/* Voice Providers account settings section: Powerline priority bar, provider rows,
 * dedicated voice API key editors, and OpenRouter (Voice) shared-key modal. */
(function () {
  "use strict";

  var DEFAULT_PROVIDER_ROWS = [
    { id: "native", av: "OS", labelKey: "js.settings.voice.native_label", rowKey: "js.settings.voice.native_row" },
    { id: "openrouter", av: "OR", labelKey: "js.settings.voice.openrouter_label", rowKey: "js.settings.voice.openrouter_row" },
    { id: "openai_api", av: "OA", labelKey: "js.settings.voice.openai_label", rowKey: "js.settings.voice.openai_row", placeholder: "sk-…", prefix: "sk-", descKey: "js.settings.account.openai_api_description" },
    { id: "groq", av: "GQ", labelKey: "js.settings.voice.groq_label", rowKey: "js.settings.voice.groq_row", placeholder: "gsk_…", prefix: "gsk_", descKey: "js.settings.account.groq_description" }
  ];

  function parseOrderString(raw) {
    if (typeof raw !== "string") return [];
    var parts = raw.trim().split(/\s+/).filter(Boolean);
    var seen = Object.create(null);
    var out = [];
    for (var i = 0; i < parts.length; i++) {
      if (!seen[parts[i]]) {
        seen[parts[i]] = true;
        out.push(parts[i]);
      }
    }
    return out;
  }

  function hasGlobalOverride(entry) {
    if (!entry || !Array.isArray(entry.layers)) return false;
    for (var i = 0; i < entry.layers.length; i++) {
      if (entry.layers[i] && entry.layers[i].scope === "global" && entry.layers[i].present) return true;
    }
    return false;
  }

  function computeEffectiveVoiceConfig(entries) {
    var byKey = Object.create(null);
    (entries || []).forEach(function (e) { if (e && e.key) byKey[e.key] = e; });
    var orderEntry = byKey["dashboard.voice_backend_order"];
    var legacyEntry = byKey["dashboard.voice_backend"];
    var sharedEntry = byKey["dashboard.voice_openrouter_shared_key"];

    var storedOrder = parseOrderString(orderEntry ? String(orderEntry.value || "") : "");
    var legacyBackend = legacyEntry ? String(legacyEntry.value || "").trim() : "";
    var order = storedOrder.length ? storedOrder : (legacyBackend ? [legacyBackend] : ["native"]);

    var sharedExplicit = hasGlobalOverride(sharedEntry);
    var sharedRaw = !!(sharedEntry && (sharedEntry.value === true || sharedEntry.value === "true"));
    var sharedEffective = sharedExplicit
      ? sharedRaw
      : (!storedOrder.length && legacyBackend === "openrouter");

    return {
      order: order,
      storedOrder: storedOrder,
      legacyBackend: legacyBackend,
      sharedExplicit: sharedExplicit,
      sharedEffective: sharedEffective
    };
  }

  function create(deps) {
    var el = deps.el;
    var button = deps.button;
    var append = deps.append;
    var request = deps.request;
    var accountRequest = deps.accountRequest;
    var hold = deps.hold;
    var message = deps.message;
    var password = deps.password;
    var field = deps.field;
    var errorText = deps.errorText;
    var getStatus = deps.getStatus;
    var getEndpoints = deps.getEndpoints;
    var onChange = deps.onChange || function () {};
    var onOpenLocal = deps.onOpenLocal || function () {};
    var onOpenAccount = typeof deps.onOpenAccount === "function" ? deps.onOpenAccount : null;
    var doc = deps.document || document;
    var win = deps.window || window;
    var t = function (key) {
      var out = typeof win.wsT === "function" ? win.wsT.apply(win, arguments) : key;
      for (var i = 1; i < arguments.length; i++) {
        out = String(out).replace(/%[sd]/, String(arguments[i]));
      }
      return out;
    };
    var btn = function (label, action, primary) {
      return button("btn" + (primary ? " pri" : ""), label, action);
    };

    var sectionEl = el("section", "vsec");
    sectionEl.dataset.voiceSection = "1";
    sectionEl.tabIndex = -1;
    var headingId = "voice-providers-title";
    var head = el("div", "vsec-head");
    var titleEl = el("h4", "", t("js.settings.voice.section_title"));
    titleEl.id = headingId;
    sectionEl.setAttribute("aria-labelledby", headingId);
    head.append(titleEl, el("p", "vsec-sub", t("js.settings.voice.section_subtitle")));

    var plWrap = el("div", "vpl-wrap");
    var plEl = el("div", "vpl");
    plEl.setAttribute("role", "list");
    plEl.setAttribute("aria-label", t("js.settings.voice.priority_aria"));
    plWrap.append(plEl);

    var hintEl = el("div", "vpl-hint");
    hintEl.append(
      el("span", "vpl-kbd", t("js.settings.voice.kbd_drag")),
      " " + t("js.settings.voice.hint_or") + " ",
      el("span", "vpl-kbd", "←"),
      el("span", "vpl-kbd", "→"),
      " " + t("js.settings.voice.hint_reorder")
    );
    var liveEl = el("div", "vsec-sr");
    liveEl.setAttribute("aria-live", "polite");
    var noticeEl = message("", "fail");
    noticeEl.hidden = true;
    var rowsEl = el("div", "vprows");

    sectionEl.append(head, plWrap, hintEl, liveEl, noticeEl, rowsEl);

    var voiceCfg = {
      order: ["native"],
      storedOrder: [],
      legacyBackend: "",
      sharedExplicit: false,
      sharedEffective: false
    };
    var nativeStatus = readNativeStatus();
    var nativeUnsub = null;
    var dragId = null;
    var dropIdx = -1;
    var pendingFocusId = null;
    var savingOrder = false;
    var keyState = Object.create(null); // id -> { busy, error, form }
    var modal = { open: false, shared: false, key: "", saving: false, error: "", scrim: null, dlg: null };

    function readNativeStatus() {
      var n = win.WsVoiceNative;
      if (n && typeof n.getStatus === "function") return n.getStatus();
      return { desktop: false, available: false, on_device: false, locale: "", reason: "desktop_only", ready: false };
    }

    function voiceKeyInfo(id) {
      var st = getStatus();
      var list = st && Array.isArray(st.voice_keys) ? st.voice_keys : [];
      for (var i = 0; i < list.length; i++) {
        if (list[i] && list[i].provider === id) return list[i];
      }
      return null;
    }

    function topOpenRouterInfo() {
      var st = getStatus();
      return (st && st.openrouter) || null;
    }

    function localEndpointNames() {
      var eps = getEndpoints();
      if (!eps || typeof eps !== "object") return [];
      return Object.keys(eps).sort();
    }

    function providerMeta(id) {
      for (var i = 0; i < DEFAULT_PROVIDER_ROWS.length; i++) {
        if (DEFAULT_PROVIDER_ROWS[i].id === id) {
          var s = DEFAULT_PROVIDER_ROWS[i];
          return { id: s.id, av: s.av, label: t(s.labelKey), row: t(s.rowKey), spec: s };
        }
      }
      if (id.indexOf("local/") === 0) {
        var name = id.slice(6);
        return { id: id, av: "LO", label: name || t("js.settings.voice.local_label"), row: t("js.settings.voice.local_row") + " (" + name + ")", spec: null };
      }
      return { id: id, av: id.slice(0, 2).toUpperCase(), label: id, row: id, spec: null };
    }

    function nativeReasonCopy(reason) {
      if (!nativeStatus.desktop || reason === "desktop_only") {
        return {
          seg: t("js.settings.voice.desktop_only"),
          pill: t("js.settings.voice.desktop_only"),
          tone: "off",
          hint: t("js.settings.voice.native_desktop_only_hint")
        };
      }
      if (reason === "permission_denied") {
        return {
          seg: t("js.settings.voice.native_permission_short"),
          pill: t("js.settings.voice.native_permission_pill"),
          tone: "warn",
          hint: t("js.settings.voice.native_permission_hint")
        };
      }
      if (reason === "privacy_disabled") {
        return {
          seg: t("js.settings.voice.native_privacy_short"),
          pill: t("js.settings.voice.native_privacy_pill"),
          tone: "warn",
          hint: t("js.settings.voice.native_privacy_hint")
        };
      }
      if (reason === "language_unavailable" || reason === "offline_pack_missing") {
        return {
          seg: t("js.settings.voice.native_lang_short"),
          pill: t("js.settings.voice.native_lang_pill"),
          tone: "warn",
          hint: t("js.settings.voice.native_lang_hint")
        };
      }
      return {
        seg: t("js.settings.voice.unavailable"),
        pill: t("js.settings.voice.unavailable"),
        tone: "warn",
        hint: reason ? t("js.settings.voice.native_unavailable_reason", reason) : t("js.settings.voice.native_unavailable_hint")
      };
    }

    function evalProvider(id) {
      if (id === "native") {
        var avail = !!(nativeStatus.desktop && nativeStatus.available);
        return {
          configured: true,
          available: avail,
          desktop: !!nativeStatus.desktop,
          reason: nativeStatus.reason || (nativeStatus.desktop ? "" : "desktop_only")
        };
      }
      if (id === "openrouter") {
        var top = topOpenRouterInfo();
        var topConfigured = !!(top && top.configured);
        var shared = !!(voiceCfg.sharedEffective && topConfigured);
        var ded = voiceKeyInfo("openrouter_voice");
        var dedConfigured = !!(ded && ded.configured);
        var mode = shared ? "shared" : (dedConfigured ? "dedicated" : "none");
        return {
          configured: mode !== "none",
          available: mode !== "none",
          mode: mode,
          sharedFlag: !!voiceCfg.sharedEffective,
          topConfigured: topConfigured,
          topMasked: (top && top.masked) || "",
          masked: (ded && ded.masked) || ""
        };
      }
      if (id.indexOf("local/") === 0) {
        var name = id.slice(6);
        var eps = getEndpoints();
        var exists = !!(eps && Object.prototype.hasOwnProperty.call(eps, name));
        return { configured: exists, available: exists };
      }
      var k = voiceKeyInfo(id);
      return { configured: !!(k && k.configured), available: !!(k && k.configured), masked: (k && k.masked) || "" };
    }

    function buildChain() {
      var list = [];
      var activeId = null;
      var order = voiceCfg.order.length ? voiceCfg.order : ["native"];
      for (var i = 0; i < order.length; i++) {
        var id = order[i];
        var ev = evalProvider(id);
        if (!ev.configured && id !== "native") continue;
        var st = ev.available ? (activeId ? "standby" : "active") : "unavailable";
        if (st === "active") activeId = id;
        list.push({ id: id, state: st, ev: ev });
      }
      return { chain: list, activeId: activeId };
    }

    function setNotice(text) {
      noticeEl.textContent = text || "";
      noticeEl.hidden = !text;
    }

    function saveGlobalConfig(key, value) {
      return request("/api/config/set", {
        scope: "global",
        key: key,
        selectors: {},
        value: value
      });
    }

    function persistOrder(nextOrder, announceText) {
      if (savingOrder) return Promise.resolve();
      savingOrder = true;
      setNotice("");
      var clean = [];
      var seen = Object.create(null);
      for (var i = 0; i < nextOrder.length; i++) {
        var id = String(nextOrder[i] || "").trim();
        if (id && !seen[id]) {
          seen[id] = true;
          clean.push(id);
        }
      }
      if (!clean.length) clean = ["native"];
      var prevOrder = voiceCfg.order.slice();
      voiceCfg.order = clean;
      voiceCfg.storedOrder = clean.slice();
      render();
      return saveGlobalConfig("dashboard.voice_backend_order", clean.join(" ")).then(function () {
        if (announceText) liveEl.textContent = announceText;
        onChange();
      }, function (err) {
        voiceCfg.order = prevOrder;
        setNotice(errorText(err && err.code));
        render();
      }).finally(function () {
        savingOrder = false;
      });
    }

    function ensureInOrder(id) {
      if (!id) return Promise.resolve();
      if (voiceCfg.order.indexOf(id) >= 0 && voiceCfg.storedOrder.length > 0) return Promise.resolve();
      var next = voiceCfg.order.slice();
      if (next.indexOf(id) < 0) next.push(id);
      return persistOrder(next);
    }

    function moveInChain(id, toIndex) {
      var built = buildChain();
      var shown = built.chain.map(function (c) { return c.id; });
      var from = shown.indexOf(id);
      if (from < 0) return;
      var to = Math.max(0, Math.min(shown.length - 1, toIndex));
      if (to === from) return;
      shown.splice(from, 1);
      shown.splice(to, 0, id);
      var k = 0;
      var next = voiceCfg.order.map(function (oid) {
        return shown.indexOf(oid) >= 0 ? shown[k++] : oid;
      });
      for (var j = k; j < shown.length; j++) {
        if (next.indexOf(shown[j]) < 0) next.push(shown[j]);
      }
      pendingFocusId = id;
      var meta = providerMeta(id);
      persistOrder(next, t("js.settings.voice.moved_announce", meta.label, String(to + 1)));
    }

    function renderPowerline() {
      plEl.replaceChildren();
      var built = buildChain();
      var chain = built.chain;
      if (!chain.length) {
        plEl.append(el("div", "vpl-empty", t("js.settings.voice.chain_empty")));
        return;
      }
      chain.forEach(function (item, idx) {
        var meta = providerMeta(item.id);
        var info = item.state === "active"
          ? t("js.settings.voice.state_active")
          : item.state === "standby"
            ? t("js.settings.voice.state_standby")
            : (item.id === "native" ? nativeReasonCopy(item.ev.reason).seg : t("js.settings.voice.unavailable"));

        var seg = el("div", "vpl-seg is-" + item.state);
        seg.setAttribute("role", "listitem");
        seg.tabIndex = 0;
        seg.draggable = true;
        seg.dataset.voiceId = item.id;
        seg.setAttribute("aria-label", t("js.settings.voice.segment_aria", String(idx + 1), meta.label, info));

        var sn = el("div", "vpl-sn");
        sn.append(el("span", "vpl-num", String(idx + 1)), el("span", "vpl-nm", meta.label));
        var si = el("div", "vpl-si", info);

        var mv = el("span", "vpl-mv");
        var leftBtn = button("", "‹", function (e) {
          if (e && e.stopPropagation) e.stopPropagation();
          moveInChain(item.id, idx - 1);
        });
        leftBtn.setAttribute("aria-label", t("js.settings.voice.move_left"));
        leftBtn.title = t("js.settings.voice.move_left");
        leftBtn.disabled = idx === 0;
        var rightBtn = button("", "›", function (e) {
          if (e && e.stopPropagation) e.stopPropagation();
          moveInChain(item.id, idx + 1);
        });
        rightBtn.setAttribute("aria-label", t("js.settings.voice.move_right"));
        rightBtn.title = t("js.settings.voice.move_right");
        rightBtn.disabled = idx === chain.length - 1;
        mv.append(leftBtn, rightBtn);

        seg.append(sn, si, mv);
        plEl.append(seg);
      });
      var dropbar = el("div", "vpl-dropbar");
      dropbar.dataset.voiceDropbar = "1";
      plEl.append(dropbar);

      if (pendingFocusId) {
        var target = plEl.querySelector('[data-voice-id="' + pendingFocusId + '"]');
        if (target && typeof target.focus === "function") target.focus();
        pendingFocusId = null;
      }
    }

    function bindPowerlineEvents() {
      function segments() {
        return Array.prototype.slice.call(plEl.querySelectorAll(".vpl-seg"));
      }
      function indexAt(clientX) {
        var segs = segments();
        for (var i = 0; i < segs.length; i++) {
          var r = segs[i].getBoundingClientRect();
          if (clientX < r.left + r.width / 2) return i;
        }
        return segs.length;
      }
      function showDropbar(i) {
        var bar = plEl.querySelector("[data-voice-dropbar]");
        var segs = segments();
        if (!bar || !segs.length) return;
        var plr = plEl.getBoundingClientRect();
        var x = i >= segs.length
          ? segs[segs.length - 1].getBoundingClientRect().right - plr.left - 6
          : segs[i].getBoundingClientRect().left - plr.left + (i === 0 ? 2 : 8);
        bar.style.left = x + "px";
        bar.style.display = "block";
      }
      function hideDropbar() {
        var bar = plEl.querySelector("[data-voice-dropbar]");
        if (bar) bar.style.display = "none";
      }

      plEl.addEventListener("dragstart", function (e) {
        var seg = e.target && e.target.closest ? e.target.closest(".vpl-seg") : null;
        if (!seg) return;
        dragId = seg.dataset.voiceId;
        seg.classList.add("is-drag");
        if (e.dataTransfer) {
          e.dataTransfer.effectAllowed = "move";
          try { e.dataTransfer.setData("text/plain", dragId); } catch (_) {}
        }
      });
      plEl.addEventListener("dragover", function (e) {
        if (!dragId) return;
        e.preventDefault();
        if (e.dataTransfer) e.dataTransfer.dropEffect = "move";
        dropIdx = indexAt(e.clientX || 0);
        showDropbar(dropIdx);
      });
      plEl.addEventListener("dragleave", function (e) {
        if (!plEl.contains(e.relatedTarget)) hideDropbar();
      });
      plEl.addEventListener("drop", function (e) {
        if (!dragId) return;
        e.preventDefault();
        var built = buildChain();
        var shown = built.chain.map(function (c) { return c.id; });
        var from = shown.indexOf(dragId);
        var to = dropIdx > from ? dropIdx - 1 : dropIdx;
        var id = dragId;
        dragId = null;
        dropIdx = -1;
        hideDropbar();
        if (from >= 0 && to !== from) moveInChain(id, to);
        else renderPowerline();
      });
      plEl.addEventListener("dragend", function () {
        dragId = null;
        dropIdx = -1;
        hideDropbar();
        segments().forEach(function (s) { s.classList.remove("is-drag"); });
      });
      plEl.addEventListener("keydown", function (e) {
        var seg = e.target && e.target.closest ? e.target.closest(".vpl-seg") : null;
        if (!seg || e.target !== seg) return;
        var built = buildChain();
        var shown = built.chain.map(function (c) { return c.id; });
        var idx = shown.indexOf(seg.dataset.voiceId);
        if (idx < 0) return;
        if (e.key === "ArrowLeft") {
          e.preventDefault();
          moveInChain(seg.dataset.voiceId, idx - 1);
        } else if (e.key === "ArrowRight") {
          e.preventDefault();
          moveInChain(seg.dataset.voiceId, idx + 1);
        }
      });
    }

    function makePill(text, tone) {
      return el("span", "vprow-pill " + (tone || "off"), text);
    }

    function closeKeyForm(id) {
      var st = keyState[id];
      if (!st || !st.form) return;
      st.form.input.value = "";
      st.form.form.remove();
      st.form = null;
    }

    function showKeyForm(spec, row) {
      var st = keyState[spec.id] || (keyState[spec.id] = { busy: false, error: "", form: null });
      if (st.form || st.busy) return;
      st.error = "";
      var input = password("account-voice-" + spec.id, spec.placeholder);
      input.required = true;
      var hint = message("");
      var form = el("form", "account-drawer account-voice-form vprow-drawer");
      form.autocomplete = "off";
      var save = btn(t("js.settings.save"), null, true);
      save.type = "submit";
      var cancel = btn(t("js.settings.account.cancel"), function () {
        closeKeyForm(spec.id);
        renderRows();
      });
      var error = message("", "fail");
      error.hidden = true;
      var configured = !!(voiceKeyInfo(spec.id) && voiceKeyInfo(spec.id).configured);
      form.append(
        append(el("div", "account-form"),
          field(t(configured ? "js.settings.account.replace_voice_key" : "js.settings.account.voice_key_label", t(spec.rowKey)), input, hint),
          append(el("div", "account-actions"), save, cancel)),
        error
      );
      var editor = { form: form, input: input, save: save, cancel: cancel, hint: hint, error: error };
      function syncHint() {
        error.hidden = true;
        var unusual = input.value.length > 0 && spec.prefix && input.value.indexOf(spec.prefix) !== 0;
        hint.textContent = unusual ? t("js.settings.account.voice_prefix_warning", spec.prefix) : t("js.settings.account.voice_key_help");
        hint.classList.toggle("warn", !!unusual);
        input.removeAttribute("aria-invalid");
      }
      input.addEventListener("input", syncHint);
      form.addEventListener("submit", function (event) {
        event.preventDefault();
        if (input.value && !st.busy) changeVoiceKey(spec, "set", editor);
      });
      syncHint();
      st.form = editor;
      row.append(form);
      input.focus();
    }

    function updateStatusVoiceKey(providerId, statusObj) {
      var st = getStatus();
      if (!st) return;
      var list = Array.isArray(st.voice_keys) ? st.voice_keys.slice() : [];
      var idx = list.findIndex(function (item) { return item && item.provider === providerId; });
      if (idx >= 0) list[idx] = statusObj;
      else list.push(statusObj);
      st.voice_keys = list;
    }

    function changeVoiceKey(spec, op, editor) {
      var st = keyState[spec.id] || (keyState[spec.id] = { busy: false, error: "", form: null });
      if (st.busy) return Promise.resolve();
      st.busy = true;
      st.error = "";
      if (editor) {
        editor.input.disabled = editor.save.disabled = editor.cancel.disabled = true;
        editor.save.textContent = t("js.settings.saving");
        editor.error.hidden = true;
      }
      renderRows();
      var body = { op: op, provider: spec.id };
      if (editor) body.key = editor.input.value;
      var req = accountRequest("key", body);
      delete body.key;
      return req.then(function (result) {
        if (editor && st.form === editor) closeKeyForm(spec.id);
        if (result && result.status) updateStatusVoiceKey(spec.id, result.status);
        var followup = op === "set" ? ensureInOrder(spec.id) : Promise.resolve();
        return followup.then(function () {
          render();
          onChange();
        });
      }, function (err) {
        st.error = errorText(err && err.code);
        if (editor && st.form === editor) {
          editor.error.hidden = false;
          editor.error.textContent = st.error;
          if (err && err.code === "invalid_key") editor.input.setAttribute("aria-invalid", "true");
        }
      }).finally(function () {
        st.busy = false;
        if (editor && st.form === editor) {
          editor.input.disabled = editor.save.disabled = editor.cancel.disabled = false;
          editor.save.textContent = t("js.settings.save");
        }
        render();
      });
    }

    // --- OpenRouter (Voice) modal ---
    function closeOpenRouterModal() {
      if (!modal.open) return;
      modal.open = false;
      modal.key = "";
      if (modal.scrim) {
        modal.scrim.remove();
        modal.scrim = null;
        modal.dlg = null;
      }
      var trigger = rowsEl.querySelector('[data-voice-provider="openrouter"] button');
      if (trigger && typeof trigger.focus === "function") trigger.focus();
    }

    function openOpenRouterModal() {
      var ev = evalProvider("openrouter");
      modal.open = true;
      modal.shared = !!(ev.topConfigured && voiceCfg.sharedEffective);
      modal.key = "";
      modal.saving = false;
      modal.error = "";
      if (!modal.scrim) {
        var scrim = el("div", "vmodal-scrim");
        scrim.dataset.voiceOrModal = "1";
        var dlg = el("div", "vmodal-dlg");
        dlg.setAttribute("role", "dialog");
        dlg.setAttribute("aria-modal", "true");
        dlg.setAttribute("aria-labelledby", "voice-or-modal-title");
        scrim.append(dlg);
        scrim.addEventListener("mousedown", function (e) {
          if (e.target === scrim && !modal.saving) closeOpenRouterModal();
        });
        doc.body.append(scrim);
        modal.scrim = scrim;
        modal.dlg = dlg;
      }
      renderOpenRouterModal();
    }

    function saveOpenRouterModal(removeDedicated) {
      if (modal.saving) return;
      var ev = evalProvider("openrouter");
      var useShared = !!(ev.topConfigured && modal.shared);
      var rawKey = removeDedicated ? "" : String(modal.key || "").trim();
      if (!useShared && rawKey && rawKey.indexOf("sk-or-") !== 0) {
        modal.error = t("js.settings.voice.or_key_prefix_error");
        renderOpenRouterModal();
        return;
      }
      modal.saving = true;
      modal.error = "";
      renderOpenRouterModal();

      // Always materialize dashboard.voice_openrouter_shared_key on save (T0 migration).
      saveGlobalConfig("dashboard.voice_openrouter_shared_key", useShared).then(function () {
        voiceCfg.sharedExplicit = true;
        voiceCfg.sharedEffective = useShared;
        if (!useShared && rawKey) {
          var body = { op: "set", provider: "openrouter_voice", key: rawKey };
          var req = accountRequest("key", body);
          delete body.key;
          return req.then(function (res) {
            if (res && res.status) updateStatusVoiceKey("openrouter_voice", res.status);
          });
        }
        if (!useShared && removeDedicated) {
          return accountRequest("key", { op: "remove", provider: "openrouter_voice" }).then(function (res) {
            if (res && res.status) updateStatusVoiceKey("openrouter_voice", res.status);
          });
        }
      }).then(function () {
        var after = evalProvider("openrouter");
        if (after.configured) return ensureInOrder("openrouter");
      }).then(function () {
        modal.saving = false;
        closeOpenRouterModal();
        render();
        onChange();
      }, function (err) {
        modal.saving = false;
        modal.error = errorText(err && err.code);
        renderOpenRouterModal();
      });
    }

    function renderOpenRouterModal() {
      if (!modal.open || !modal.dlg) return;
      var dlg = modal.dlg;
      dlg.replaceChildren();
      var ev = evalProvider("openrouter");
      var topConfigured = ev.topConfigured;
      var shared = !!(topConfigured && modal.shared);

      var title = el("h4", "", t("js.settings.voice.or_modal_title"));
      title.id = "voice-or-modal-title";
      dlg.append(title, el("p", "vmodal-sub", t("js.settings.voice.or_modal_sub")));

      // Show "Reuse Werk OpenRouter key" toggle ONLY when the Werk OpenRouter key is configured.
      if (topConfigured) {
        var swRow = el("div", "vsw-row" + (shared ? " on" : ""));
        var swLabel = el("div", "vsw-label");
        swLabel.append(
          el("b", "", t("js.settings.voice.or_use_top_label")),
          el("span", "", t("js.settings.voice.or_use_top_desc", ev.topMasked || "sk-or-…"))
        );
        var sw = button("vsw", "", function () {
          if (modal.saving) return;
          modal.shared = !modal.shared;
          modal.error = "";
          renderOpenRouterModal();
        });
        sw.dataset.voiceOrShared = "1";
        sw.setAttribute("role", "switch");
        sw.setAttribute("aria-checked", String(shared));
        sw.setAttribute("aria-label", t("js.settings.voice.or_use_top_label"));
        swRow.append(swLabel, sw);
        dlg.append(swRow, el("p", "vmodal-note", t(shared ? "js.settings.voice.or_shared_on_note" : "js.settings.voice.or_shared_off_note")));
      } else {
        dlg.append(el("p", "vmodal-note warn", t("js.settings.voice.or_no_top_key_note")));
      }

      var flbl = el("label", "vmodal-flbl", t("js.settings.voice.or_dedicated_label"));
      flbl.htmlFor = "voice-or-dedicated-key";
      var inp = el("input", "vmodal-fin");
      inp.id = "voice-or-dedicated-key";
      inp.dataset.voiceOrKeyInput = "1";
      inp.type = "password";
      inp.autocomplete = "off";
      inp.spellcheck = false;
      inp.disabled = shared || modal.saving;
      inp.placeholder = shared
        ? t("js.settings.voice.or_reusing_placeholder", ev.topMasked || "sk-or-…")
        : "sk-or-v1-…";
      inp.value = shared ? "" : modal.key;
      inp.addEventListener("input", function () {
        modal.key = inp.value;
        modal.error = "";
      });
      dlg.append(flbl, inp);

      if (ev.masked && !shared) {
        dlg.append(el("p", "vmodal-note", t("js.settings.voice.or_current_dedicated", ev.masked)));
      }
      if (modal.error) {
        var errEl = el("p", "vmodal-err", modal.error);
        errEl.setAttribute("role", "alert");
        dlg.append(errEl);
      }

      var foot = el("div", "vmodal-foot");
      if (ev.masked && !shared) {
        var removeBtn = btn(t("js.settings.voice.or_remove_dedicated"), function () {
          saveOpenRouterModal(true);
        });
        removeBtn.disabled = modal.saving;
        foot.append(removeBtn);
      }
      var cancelBtn = btn(t("js.settings.account.cancel"), closeOpenRouterModal);
      cancelBtn.disabled = modal.saving;
      var saveBtn = btn(modal.saving ? t("js.settings.saving") : t("js.settings.save"), function () {
        saveOpenRouterModal(false);
      }, true);
      saveBtn.dataset.voiceOrSave = "1";
      saveBtn.disabled = modal.saving;
      foot.append(cancelBtn, saveBtn);
      dlg.append(foot);
    }

    function renderRows() {
      rowsEl.replaceChildren();
      DEFAULT_PROVIDER_ROWS.forEach(function (spec) {
        var id = spec.id;
        var ev = evalProvider(id);
        var row = el("div", "vprow");
        row.dataset.voiceProvider = id;
        row.tabIndex = -1;

        var av = el("span", "vprow-av", spec.av);
        var main = el("div", "vprow-main");
        var nameLine = el("div", "vprow-name", t(spec.rowKey));
        var hint = el("div", "vprow-hint");
        var acts = el("div", "vprow-acts");

        if (id === "native") {
          if (ev.available) {
            nameLine.append(makePill(t("js.settings.voice.native_available"), "ok"));
            hint.textContent = t(nativeStatus.on_device ? "js.settings.voice.native_on_device_hint" : "js.settings.voice.native_available_hint");
          } else {
            var nc = nativeReasonCopy(ev.reason);
            if (!ev.desktop) row.classList.add("is-dimmed");
            nameLine.append(makePill(nc.pill, nc.tone));
            hint.textContent = nc.hint;
          }
        } else if (id === "openrouter") {
          if (ev.mode === "shared") {
            nameLine.append(makePill(t("js.settings.voice.or_status_shared"), "pri"));
            hint.textContent = t("js.settings.voice.or_hint_shared", ev.topMasked || "sk-or-…");
          } else if (ev.mode === "dedicated") {
            nameLine.append(makePill(t("js.settings.voice.or_status_dedicated"), "ok"));
            hint.textContent = t("js.settings.voice.or_hint_dedicated", ev.masked || "");
          } else {
            nameLine.append(makePill(t("js.settings.account.unset"), "off"));
            hint.textContent = t(ev.sharedFlag && !ev.topConfigured
              ? "js.settings.voice.or_hint_shared_missing_top"
              : "js.settings.voice.or_hint_unset");
          }
          acts.append(btn(t("js.settings.voice.configure"), openOpenRouterModal));
          if (onOpenAccount) {
            var accountBtn = button("vprow-chip-link", t("js.settings.voice.manage_shared_key"), function () {
              onOpenAccount();
            });
            accountBtn.dataset.voiceOpenAccount = "1";
            acts.append(accountBtn);
          }
        } else {
          var st = keyState[id] || { busy: false, error: "", form: null };
          if (ev.configured) {
            nameLine.append(makePill(t("js.settings.account.key_set") + (ev.masked ? " · " + ev.masked : ""), "ok"));
            hint.textContent = t(spec.descKey);
            if (!st.form) {
              acts.append(
                btn(t("js.settings.account.replace_key"), function () { showKeyForm(spec, row); }),
                hold("i-trash", t("js.settings.account.remove_voice_key", t(spec.rowKey)), function () { changeVoiceKey(spec, "remove"); })
              );
            }
          } else {
            nameLine.append(makePill(t("js.settings.account.unset"), "off"));
            hint.textContent = t(spec.descKey);
            if (!st.form) {
              acts.append(btn(t("js.settings.account.add_key"), function () { showKeyForm(spec, row); }));
            }
          }
          if (st.busy) acts.querySelectorAll("button").forEach(function (b) { b.disabled = true; });
          if (st.error && !st.form) main.append(message(st.error, "fail"));
        }

        main.prepend(nameLine, hint);
        row.append(av, main, acts);
        if (keyState[id] && keyState[id].form) row.append(keyState[id].form.form);
        rowsEl.append(row);
      });

      // Local STT endpoints summary row.
      var locals = localEndpointNames();
      var localRow = el("div", "vprow");
      localRow.dataset.voiceProvider = "local";
      localRow.tabIndex = -1;
      var lAv = el("span", "vprow-av", "LO");
      var lMain = el("div", "vprow-main");
      var lName = el("div", "vprow-name", t("js.settings.voice.local_row"));
      var lHint = el("div", "vprow-hint");
      var lActs = el("div", "vprow-acts");
      if (locals.length) {
        lName.append(makePill(t("js.settings.account.endpoint_count", locals.length), "ok"));
        lHint.textContent = t("js.settings.voice.local_configured_hint", locals.map(function (n) { return "local/" + n; }).join(", "));
        var firstLocalId = "local/" + locals[0];
        if (voiceCfg.order.indexOf(firstLocalId) < 0) {
          lActs.append(btn(t("js.settings.voice.add_to_order"), function () { ensureInOrder(firstLocalId); }));
        }
      } else {
        lName.append(makePill(t("js.settings.account.unset"), "off"));
        lHint.textContent = t("js.settings.voice.local_empty_hint");
      }
      lActs.append(btn(t("js.settings.voice.manage_local"), onOpenLocal));
      lMain.append(lName, lHint);
      localRow.append(lAv, lMain, lActs);
      rowsEl.append(localRow);
    }

    function render() {
      renderPowerline();
      renderRows();
      if (modal.open) renderOpenRouterModal();
    }

    function applyConfigSnapshot(config) {
      if (!config || !Array.isArray(config.entries)) return;
      voiceCfg = computeEffectiveVoiceConfig(config.entries);
      render();
    }

    function refreshConfig() {
      return request("/api/config/snapshot?scope=global").then(function (cfg) {
        applyConfigSnapshot(cfg);
      }, function () {});
    }

    function onDocKeyDown(e) {
      if (e && e.key === "Escape" && modal.open && !modal.saving) {
        closeOpenRouterModal();
      }
    }

    function onNativeStatus(snap) {
      nativeStatus = snap || readNativeStatus();
      render();
    }

    bindPowerlineEvents();
    if (doc && doc.addEventListener) doc.addEventListener("keydown", onDocKeyDown);
    if (win.WsVoiceNative && typeof win.WsVoiceNative.subscribe === "function") {
      nativeUnsub = win.WsVoiceNative.subscribe(onNativeStatus);
    }
    render();

    return {
      element: sectionEl,
      render: render,
      applyConfigSnapshot: applyConfigSnapshot,
      refreshConfig: refreshConfig,
      isSharedOpenRouterKey: function () {
        return evalProvider("openrouter").mode === "shared";
      },
      closeEditors: function () {
        closeOpenRouterModal();
        Object.keys(keyState).forEach(closeKeyForm);
        render();
      },
      focus: function (provider) {
        if (provider === "voice") {
          sectionEl.classList.remove("vsec-focus");
          sectionEl.classList.add("vsec-focus");
          if (typeof sectionEl.focus === "function") sectionEl.focus({ preventScroll: true });
          if (typeof sectionEl.scrollIntoView === "function") sectionEl.scrollIntoView({ block: "nearest" });
          return true;
        }
        var targetId = provider === "openrouter_voice" ? "openrouter" : provider;
        var row = rowsEl.querySelector('[data-voice-provider="' + targetId + '"]');
        if (row) {
          if (typeof row.focus === "function") row.focus({ preventScroll: true });
          if (typeof row.scrollIntoView === "function") row.scrollIntoView({ block: "nearest" });
          return true;
        }
        return false;
      },
      destroy: function () {
        closeOpenRouterModal();
        if (nativeUnsub) { nativeUnsub(); nativeUnsub = null; }
        if (doc && doc.removeEventListener) doc.removeEventListener("keydown", onDocKeyDown);
      }
    };
  }

  window.WsSettingsVoice = {
    create: create,
    parseOrderString: parseOrderString,
    computeEffectiveVoiceConfig: computeEffectiveVoiceConfig
  };
})();
