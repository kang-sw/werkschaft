/* Group 1 of Settings -> Account: External CLI Harness Accounts (Claude Code, Codex CLI, Antigravity). */
(function () {
  "use strict";

  const HARNESS_ORDER = ["claude", "codex"];
  const HARNESS_META = {
    claude: {title: "Claude Code", monogram: "C", iconClass: "ic-claude"},
    codex: {title: "Codex CLI", monogram: "X", iconClass: "ic-codex"}
  };

  function orderedHarnesses(slotsMap) {
    if (!slotsMap || typeof slotsMap !== "object") return [];
    return HARNESS_ORDER.filter(name => Object.hasOwn(slotsMap, name));
  }

  function create(ctx) {
    const {el, append, svgIcon, t, h, nav, accountRequest, pending, refreshStatus} = ctx;
    const group = h.groupCard("1", t("js.settings.account.group_harness_title"), t("js.settings.account.group_harness_subtitle"));
    group.root.dataset.accountGroup = "harness";
    group.root.hidden = true;

    let slotSignIn = null, slotPollTimer = null, slotBusy = false, slotError = "", slotErrorHarness = "";
    let slotAddForm = null;
    let renameState = null; // {harness, slotId, form, input, saveBtn, cancelBtn, errEl, busy}
    let harnessAuthSignIn = null, harnessAuthPollTimer = null, harnessAuthBusy = false, harnessAuthError = "";

    function scheduleSlotPoll() {
      if (slotPollTimer || !pending(slotSignIn)) return;
      slotPollTimer = window.setTimeout(pollSlotSignIn, 900);
    }

    async function pollSlotSignIn() {
      slotPollTimer = null;
      if (!pending(slotSignIn)) return;
      try {
        const before = slotSignIn;
        const result = await accountRequest("slots/sign-in/status");
        if (before !== slotSignIn) return;
        if (JSON.stringify(result) === JSON.stringify(before) && !slotError) return;
        slotSignIn = result;
        slotError = "";
        slotErrorHarness = "";
        render();
        if (result.state === "succeeded") await refreshStatus();
      } catch (_) {
        if (!slotError) {
          slotError = t("js.settings.account.progress_failed");
          render();
        }
      } finally {
        scheduleSlotPoll();
      }
    }

    async function changeSlot(path, body) {
      if (slotBusy) return;
      slotBusy = true;
      slotError = "";
      slotErrorHarness = body?.harness || "";
      render();
      try {
        const result = await accountRequest(path, body);
        if (path.startsWith("slots/sign-in/")) {
          slotSignIn = result;
          scheduleSlotPoll();
        }
        if (path === "slots/add") closeSlotAddEditor();
      } catch (error) {
        slotError = h.errorText(error.code);
        slotErrorHarness = body?.harness || "";
      } finally {
        slotBusy = false;
        if (path === "slots/switch" || path === "slots/add" || path === "slots/remove") await refreshStatus();
        render();
      }
    }

    function closeSlotAddEditor() {
      if (!slotAddForm) return;
      slotAddForm.form.remove();
      slotAddForm = null;
    }

    function showSlotAddEditor(harness) {
      if (slotBusy) return;
      closeSlotAddEditor();
      slotError = "";
      slotErrorHarness = "";
      const form = el("form", "acc-drawer");
      form.autocomplete = "off";
      const labelInp = el("input", "inp acc-inp");
      labelInp.id = "account-slot-label-" + harness;
      labelInp.placeholder = t("js.settings.account.slot_label_placeholder");
      labelInp.maxLength = 80;
      const dirInp = el("input", "inp mono acc-inp");
      dirInp.id = "account-slot-dir-" + harness;
      dirInp.placeholder = t("js.settings.account.slot_dir_placeholder");
      dirInp.spellcheck = false;
      dirInp.required = true;
      const dirHint = h.message(t("js.settings.account.slot_dir_help"));
      const save = h.ab(t("js.settings.account.add"), null, {cls: "solid", submit: true});
      const cancel = h.ab(t("js.settings.account.cancel"), () => {
        closeSlotAddEditor();
        render();
      });
      form.append(
        append(el("div", "acc-dr-t"), el("span", "", t("js.settings.account.slot_add_existing"))),
        append(el("div", "account-slot-form-grid"),
          h.field(t("js.settings.account.slot_label"), labelInp),
          h.field(t("js.settings.account.slot_dir_label"), dirInp, dirHint)),
        append(el("div", "acc-dr-foot"), cancel, save)
      );
      form.addEventListener("submit", event => {
        event.preventDefault();
        const dir = dirInp.value.trim();
        if (!dir || slotBusy) return;
        changeSlot("slots/add", {harness, label: labelInp.value.trim(), config_dir: dir});
      });
      slotAddForm = {harness, form, labelInp, dirInp};
      render();
      labelInp.focus();
    }

    function cancelRename(restoreToBtn) {
      if (!renameState) return;
      const {harness, slotId, form} = renameState;
      form.remove();
      renameState = null;
      render();
      if (restoreToBtn) {
        const btn = group.body.querySelector('[data-account-row="slots-' + harness + '"] [data-slot-rename="' + slotId + '"]');
        if (btn) btn.focus({preventScroll: true});
      }
    }

    function openRename(harness, slot) {
      if (slotBusy || !slot || slot.id === "default") return;
      if (renameState) {
        renameState.form.remove();
        renameState = null;
      }
      const form = el("form", "acc-row-edit");
      form.autocomplete = "off";
      const input = el("input", "inp acc-inp");
      input.id = "account-slot-rename-" + harness + "-" + slot.id;
      input.dataset.slotRenameInput = "1";
      input.maxLength = 80;
      input.value = slot.label || slot.id;
      input.setAttribute("aria-label", t("js.settings.account.slot_rename_label"));
      const saveBtn = h.ab(t("js.settings.save"), null, {cls: "pri", submit: true});
      const cancelBtn = h.ab(t("js.settings.account.cancel"), () => cancelRename(true));
      const errEl = h.message("", "fail");
      errEl.classList.add("acc-rename-err");
      errEl.hidden = true;
      form.append(input, saveBtn, cancelBtn, errEl);

      const state = {harness, slotId: slot.id, form, input, saveBtn, cancelBtn, errEl, busy: false};

      input.addEventListener("input", () => {
        errEl.hidden = true;
        errEl.textContent = "";
        input.removeAttribute("aria-invalid");
      });
      input.addEventListener("keydown", event => {
        if (event.key === "Escape") {
          event.preventDefault();
          event.stopPropagation();
          cancelRename(true);
        }
      });
      form.addEventListener("submit", async event => {
        event.preventDefault();
        if (state.busy) return;
        const trimmed = input.value.trim();
        if (!trimmed) {
          errEl.textContent = t("js.settings.account.slot_label_required");
          errEl.hidden = false;
          input.setAttribute("aria-invalid", "true");
          input.focus();
          return;
        }
        if (Array.from(trimmed).length > 80) {
          errEl.textContent = t("js.settings.account.slot_label_too_long");
          errEl.hidden = false;
          input.setAttribute("aria-invalid", "true");
          input.focus();
          return;
        }
        state.busy = true;
        input.disabled = saveBtn.disabled = cancelBtn.disabled = true;
        errEl.hidden = true;
        try {
          const updated = await accountRequest("slots/rename", {harness, slot_id: slot.id, label: trimmed});
          const list = nav.status?.slots?.[harness];
          if (Array.isArray(list)) {
            const target = list.find(item => item.id === slot.id);
            if (target) target.label = updated?.label || trimmed;
          }
          if (renameState === state) {
            form.remove();
            renameState = null;
          }
          await refreshStatus();
          render();
          const btn = group.body.querySelector('[data-account-row="slots-' + harness + '"] [data-slot-rename="' + slot.id + '"]');
          if (btn) btn.focus({preventScroll: true});
        } catch (error) {
          state.busy = false;
          if (renameState === state) {
            input.disabled = saveBtn.disabled = cancelBtn.disabled = false;
            errEl.textContent = h.errorText(error.code);
            errEl.hidden = false;
            input.setAttribute("aria-invalid", "true");
            input.focus();
          }
        }
      });

      renameState = state;
      render();
      input.focus();
      if (typeof input.select === "function") input.select();
    }

    function scheduleHarnessAuthPoll() {
      if (harnessAuthPollTimer || !pending(harnessAuthSignIn)) return;
      harnessAuthPollTimer = window.setTimeout(pollHarnessAuth, 900);
    }

    async function pollHarnessAuth() {
      harnessAuthPollTimer = null;
      if (!pending(harnessAuthSignIn)) return;
      try {
        const before = harnessAuthSignIn;
        const result = await accountRequest("harness-auth/status");
        if (before !== harnessAuthSignIn) return;
        const nextSignIn = result?.sign_in || null;
        if (Array.isArray(result?.providers) && nav.status) nav.status.harness_auth = result.providers;
        if (JSON.stringify(nextSignIn) === JSON.stringify(before) && !harnessAuthError) return;
        harnessAuthSignIn = nextSignIn;
        harnessAuthError = "";
        render();
        if (nextSignIn?.state === "succeeded") await refreshStatus();
      } catch (_) {
        if (!harnessAuthError) {
          harnessAuthError = t("js.settings.account.progress_failed");
          render();
        }
      } finally {
        scheduleHarnessAuthPoll();
      }
    }

    async function changeHarnessAuth(path, body) {
      if (harnessAuthBusy) return;
      harnessAuthBusy = true;
      harnessAuthError = "";
      render();
      try {
        const result = await accountRequest(path, body);
        if (path === "harness-auth/reverify") {
          await refreshStatus();
        } else if (result) {
          harnessAuthSignIn = result;
          scheduleHarnessAuthPoll();
        }
      } catch (error) {
        harnessAuthError = h.errorText(error.code);
      } finally {
        harnessAuthBusy = false;
        render();
      }
    }

    function buildLinkCopyRow(url, labelText) {
      const inp = el("input", "inp mono acc-inp");
      inp.readOnly = true;
      inp.value = url;
      inp.addEventListener("focus", () => { if (typeof inp.select === "function") inp.select(); });
      const copyBtn = h.ab(t("js.settings.account.copy_link"), () => h.copyText(url, copyBtn), {cls: "pri"});
      const openLink = el("a", "acc-ab", t("js.settings.account.open_link"));
      openLink.href = url;
      openLink.target = "_blank";
      openLink.rel = "noopener noreferrer";
      openLink.append(svgIcon("l-external-link"));
      return append(el("div", "account-field"), el("label", "", labelText),
        append(el("div", "account-copy-row"), inp, copyBtn, openLink));
    }

    function renderSlotDrawer(harness, preservedPaste) {
      const drawer = el("div", "acc-drawer account-signin-drawer");
      drawer.append(h.message(t("js.settings.account.slot_login_started")));
      if (slotSignIn.mode === "device_auth") {
        if (slotSignIn.verify_url) {
          drawer.append(buildLinkCopyRow(slotSignIn.verify_url, t("js.settings.account.slot_device_url_label")));
        }
        if (slotSignIn.user_code) {
          const copyCode = h.ab(t("js.settings.account.copy_code"), () => h.copyText(slotSignIn.user_code, copyCode), {cls: "pri"});
          const codeBox = append(el("div", "account-copy-row"),
            el("span", "account-device-code", slotSignIn.user_code), copyCode);
          drawer.append(append(el("div", "account-field"),
            el("label", "", t("js.settings.account.slot_device_code_label")),
            codeBox, h.message(t("js.settings.account.slot_device_help"))));
        }
      } else {
        if (slotSignIn.authorize_url) {
          drawer.append(buildLinkCopyRow(slotSignIn.authorize_url, t("js.settings.account.signin_url_label")));
        }
        if (slotSignIn.callback_ready || slotSignIn.stdin_ready || slotSignIn.authorize_url) {
          const cbForm = el("form", "account-form");
          cbForm.autocomplete = "off";
          const cbInp = el("input", "inp mono acc-inp");
          cbInp.id = "account-slot-paste-" + harness;
          cbInp.dataset.slotPaste = "1";
          cbInp.dataset.attempt = slotSignIn.attempt || "";
          cbInp.spellcheck = false;
          cbInp.autocomplete = "off";
          cbInp.value = preservedPaste;
          const submit = h.ab(t("js.settings.account.signin_submit"), null, {cls: "solid", submit: true, disabled: slotBusy});
          cbForm.append(
            h.field(t("js.settings.account.slot_callback_paste_label"), cbInp, h.message(t("js.settings.account.slot_callback_paste_help"))),
            append(el("div", "account-actions"), submit));
          cbForm.addEventListener("submit", event => {
            event.preventDefault();
            const val = cbInp.value.trim();
            if (!val || slotBusy) return;
            cbInp.value = "";
            changeSlot("slots/sign-in/complete", {attempt: slotSignIn.attempt, input: val});
          });
          drawer.append(cbForm);
        }
      }
      return drawer;
    }

    function renderHarnessBlock(harness, list, modes, preservedPaste) {
      const meta = HARNESS_META[harness] || {title: harness, monogram: harness.slice(0, 1).toUpperCase(), iconClass: "ic-codex"};
      const activeSlot = list.find(s => s.active) || list[0];
      const activeLabel = activeSlot ? (activeSlot.id === "default" ? t("js.settings.account.slot_default") : (activeSlot.label || activeSlot.id)) : "";
      const summary = list.length && activeLabel
        ? t("js.settings.account.harness_summary", activeLabel, list.length)
        : t("js.settings.account.slot_count", list.length);
      const block = h.providerBlock({
        rowName: "slots-" + harness,
        iconClass: meta.iconClass,
        monogram: meta.monogram,
        title: meta.title,
        subtitle: summary
      });
      block.subEl.title = t(harness === "claude" ? "js.settings.account.claude_slots_description" : "js.settings.account.codex_slots_description");

      const signActive = pending(slotSignIn) && slotSignIn.harness === harness;
      const anySignActive = pending(slotSignIn);
      if (signActive) {
        block.acts.append(h.busyPill(slotSignIn.state === "cancelling" ? "js.settings.account.cancelling" : "js.settings.account.signing_in"));
        block.acts.append(h.ab(t("js.settings.account.cancel"), () => changeSlot("slots/sign-in/cancel", {attempt: slotSignIn.attempt}), {
          disabled: slotSignIn.state === "cancelling" || !slotSignIn.attempt
        }));
      } else {
        if (modes.includes("browser")) {
          block.acts.append(h.ab(t("js.settings.account.slot_sign_in_browser"),
            () => changeSlot("slots/sign-in/start", {harness, mode: "browser"}),
            {cls: "pri", disabled: slotBusy || anySignActive || !nav.status?.loopback_peer}));
        }
        if (modes.includes("device_auth")) {
          block.acts.append(h.ab(t("js.settings.account.slot_sign_in_device"),
            () => changeSlot("slots/sign-in/start", {harness, mode: "device_auth"}),
            {cls: "pri", disabled: slotBusy || anySignActive || !nav.status?.loopback_peer}));
        }
        block.acts.append(h.ab(t("js.settings.account.slot_add_existing"),
          () => showSlotAddEditor(harness),
          {disabled: slotBusy || anySignActive}));
      }

      if (renameState && renameState.harness === harness && !list.some(s => s.id === renameState.slotId)) {
        renameState.form.remove();
        renameState = null;
      }

      for (const slot of list) {
        const isRenaming = !!(renameState && renameState.harness === harness && renameState.slotId === slot.id);
        const label = slot.id === "default" ? t("js.settings.account.slot_default") : (slot.label || slot.id);
        const row = el("div", "acc-row" + (slot.active ? " is-active" : ""));
        row.dataset.slotId = slot.id;
        const lead = el("div", "acc-lead");
        if (slot.active) {
          lead.append(h.pill(t("js.settings.account.active"), "on", "", true));
        } else {
          lead.append(h.ab(t("js.settings.account.use_account"),
            () => changeSlot("slots/switch", {harness, slot_id: slot.id}),
            {cls: "pri", disabled: slotBusy}));
        }
        const main = el("div", "acc-row-main");
        if (isRenaming) {
          main.append(renameState.form);
        } else {
          main.append(el("span", "acc-row-name", label));
        }
        const kindTag = slot.id === "default"
          ? t("js.settings.account.slot_default")
          : t(slot.managed ? "js.settings.account.slot_managed" : "js.settings.account.slot_custom");
        main.append(el("span", "acc-tag", kindTag));
        if (slot.config_dir) {
          const pathEl = el("span", "acc-row-sub", slot.config_dir);
          pathEl.title = slot.config_dir;
          main.append(pathEl);
        }

        const acts = el("div", "acc-row-act");
        if (slot.id !== "default") {
          if (!isRenaming) {
            const rnBtn = h.ab(t("js.settings.account.slot_rename"), () => openRename(harness, slot), {disabled: slotBusy});
            rnBtn.dataset.slotRename = slot.id;
            acts.append(rnBtn);
          }
          acts.append(h.holdBar(t("js.settings.account.remove_btn"), t("js.settings.account.slot_remove", label),
            () => changeSlot("slots/remove", {harness, slot_id: slot.id, delete_dir: !!slot.managed}),
            {disabled: slotBusy}));
        }
        row.append(lead, main, acts);
        block.kids.append(row);
      }

      if (signActive && slotSignIn.state !== "cancelling") {
        block.kids.append(renderSlotDrawer(harness, preservedPaste));
      }
      if (slotAddForm && slotAddForm.harness === harness) {
        block.kids.append(slotAddForm.form);
      }

      let slotNotice = "", slotTone = "";
      if (slotSignIn?.harness === harness && slotSignIn.state === "failed" && !pending(slotSignIn)) {
        slotNotice = h.errorText(slotSignIn.reason, "js.settings.account.slot_login_failed");
        slotTone = "fail";
      } else if (slotSignIn?.harness === harness && slotSignIn.state === "cancelled") {
        slotNotice = t("js.settings.account.sign_in_cancelled");
      }
      if (slotError && (!slotErrorHarness || slotErrorHarness === harness)) {
        slotNotice = slotError;
        slotTone = "fail";
      }
      h.setNotice(block.noticeSlot, slotNotice, slotTone);
      return block.root;
    }

    function renderSingleAuthBlock(item, preservedAuthPaste) {
      const harness = item.harness;
      const activeAttempt = pending(harnessAuthSignIn) && harnessAuthSignIn.harness === harness
        ? harnessAuthSignIn : (pending(item.sign_in) ? item.sign_in : null);
      const lastAttempt = harnessAuthSignIn?.harness === harness ? harnessAuthSignIn : item.sign_in;
      let pillLabel = "js.settings.account.status_unknown", pillTone = "unset";
      if (item.auth_required) {
        pillLabel = "js.settings.account.reauth_required";
        pillTone = "fail";
      } else if (item.probed && !item.status) {
        pillLabel = "js.settings.account.signed_in";
        pillTone = "ok";
      }
      const block = h.providerBlock({
        rowName: "harness-auth-" + harness,
        iconClass: "ic-agy",
        monogram: (item.label || harness || "A").slice(0, 1).toUpperCase(),
        title: item.label || harness,
        tag: t("js.settings.account.single_slot_tag"),
        subtitle: t(pillLabel)
      });
      block.root.classList.toggle("account-configured", !!item.probed && !item.auth_required && !item.status && !activeAttempt);

      if (activeAttempt) {
        block.acts.append(h.busyPill(activeAttempt.state === "cancelling" ? "js.settings.account.cancelling" : "js.settings.account.signing_in"));
        block.acts.append(h.ab(t("js.settings.account.cancel"),
          () => changeHarnessAuth("harness-auth/cancel", {harness, attempt: activeAttempt.attempt}),
          {disabled: harnessAuthBusy || activeAttempt.state === "cancelling" || !activeAttempt.attempt}));
      } else {
        if (item.auth_required || !item.probed || item.status) {
          block.acts.append(h.ab(t(item.auth_required ? "js.settings.account.sign_in_again" : "js.settings.account.sign_in"),
            () => changeHarnessAuth("harness-auth/start", {harness}),
            {cls: "pri", icon: "l-external-link", disabled: harnessAuthBusy || pending(harnessAuthSignIn) || !nav.status?.loopback_peer}));
        }
        block.acts.append(h.ab(t("js.settings.account.reverify"),
          () => changeHarnessAuth("harness-auth/reverify", {harness}),
          {disabled: harnessAuthBusy || item.probing || !nav.status?.loopback_peer}));
      }

      const row = el("div", "acc-row");
      const lead = append(el("div", "acc-lead"), h.pill(t("js.settings.account.single_tag"), "on"));
      const statePill = item.probing ? h.busyPill("js.settings.account.checking") : h.pill(t(pillLabel), pillTone, "", pillTone === "ok");
      const main = append(el("div", "acc-row-main"),
        el("span", "acc-row-name", item.label || harness),
        statePill,
        el("span", "acc-row-sub", t("js.settings.account.agy_auth_description")));
      row.append(lead, main);
      block.kids.append(row);

      if (activeAttempt && activeAttempt.state !== "cancelling") {
        const drawer = el("div", "acc-drawer account-signin-drawer");
        if (activeAttempt.browser_error) drawer.append(h.message(t("js.settings.account.signin_browser_fallback"), "warn"));
        drawer.append(h.message(t("js.settings.account.harness_auth_started")));
        if (activeAttempt.authorize_url) {
          drawer.append(buildLinkCopyRow(activeAttempt.authorize_url, t("js.settings.account.signin_url_label")));
        }
        if (activeAttempt.stdin_ready || activeAttempt.callback_ready || activeAttempt.authorize_url) {
          const pForm = el("form", "account-form");
          pForm.autocomplete = "off";
          const pInp = el("input", "inp mono acc-inp");
          pInp.id = "account-harness-auth-paste-" + harness;
          pInp.dataset.harnessAuthPaste = "1";
          pInp.dataset.attempt = activeAttempt.attempt || "";
          pInp.spellcheck = false;
          pInp.autocomplete = "off";
          pInp.value = preservedAuthPaste;
          const submit = h.ab(t("js.settings.account.signin_submit"), null, {cls: "solid", submit: true, disabled: harnessAuthBusy});
          pForm.append(
            h.field(t("js.settings.account.harness_auth_paste_label"), pInp, h.message(t("js.settings.account.harness_auth_paste_help"))),
            append(el("div", "account-actions"), submit));
          pForm.addEventListener("submit", event => {
            event.preventDefault();
            const val = pInp.value.trim();
            if (!val || harnessAuthBusy) return;
            pInp.value = "";
            changeHarnessAuth("harness-auth/complete", {harness, attempt: activeAttempt.attempt, input: val});
          });
          drawer.append(pForm);
        }
        block.kids.append(drawer);
      }

      let authNotice = "", authTone = "";
      if (lastAttempt?.state === "failed" && !activeAttempt) {
        authNotice = h.errorText(lastAttempt.reason, "js.settings.account.slot_login_failed");
        authTone = "fail";
      } else if (lastAttempt?.state === "cancelled" && !activeAttempt) {
        authNotice = t("js.settings.account.sign_in_cancelled");
      }
      if (harnessAuthError) {
        authNotice = harnessAuthError;
        authTone = "fail";
      }
      h.setNotice(block.noticeSlot, authNotice, authTone);
      return block.root;
    }

    function render() {
      const slotsMap = nav.status?.slots;
      const modesMap = nav.status?.slot_login_modes || {};
      const harnesses = orderedHarnesses(slotsMap);
      const singleAuths = Array.isArray(nav.status?.harness_auth)
        ? nav.status.harness_auth.filter(item => item && (item.available || item.probed || item.auth_required || pending(item.sign_in)))
        : [];
      const anyVisible = harnesses.length > 0 || singleAuths.length > 0;
      group.root.hidden = !anyVisible;
      if (!anyVisible) {
        group.body.replaceChildren();
        return;
      }

      const savedFocus = h.captureFocus(group.body);
      const existingPaste = group.body.querySelector("[data-slot-paste]");
      const preservedPaste = existingPaste && (existingPaste.dataset.attempt || "") === (slotSignIn?.attempt || "") ? existingPaste.value : "";
      const existingAuthPaste = group.body.querySelector("[data-harness-auth-paste]");
      const preservedAuthPaste = existingAuthPaste && (existingAuthPaste.dataset.attempt || "") === (harnessAuthSignIn?.attempt || "") ? existingAuthPaste.value : "";

      const nodes = [];
      for (const harness of harnesses) {
        const list = Array.isArray(slotsMap[harness]) ? slotsMap[harness] : [];
        const modes = Array.isArray(modesMap[harness]) && modesMap[harness].length ? modesMap[harness] : ["browser"];
        nodes.push(renderHarnessBlock(harness, list, modes, preservedPaste));
      }
      for (const item of singleAuths) {
        nodes.push(renderSingleAuthBlock(item, preservedAuthPaste));
      }
      group.body.replaceChildren(...nodes);
      h.restoreFocus(group.body, savedFocus);
    }

    function applyStatus(before, result) {
      const prevSlot = slotSignIn;
      const prevAuth = harnessAuthSignIn;
      if (!slotBusy && result.slot_sign_in && JSON.stringify(slotSignIn) !== JSON.stringify(result.slot_sign_in)) {
        slotSignIn = result.slot_sign_in;
      }
      if (pending(slotSignIn)) scheduleSlotPoll();
      const activeHarnessAuth = Array.isArray(result.harness_auth)
        ? result.harness_auth.find(item => pending(item.sign_in))?.sign_in ||
          result.harness_auth.find(item => item.sign_in && item.sign_in.state && item.sign_in.state !== "idle")?.sign_in
        : null;
      const nextHarnessAuth = activeHarnessAuth || {state: "idle"};
      if (!harnessAuthBusy && JSON.stringify(harnessAuthSignIn) !== JSON.stringify(nextHarnessAuth)) {
        harnessAuthSignIn = nextHarnessAuth;
      }
      if (pending(harnessAuthSignIn)) scheduleHarnessAuthPoll();
      if (prevSlot !== slotSignIn || prevAuth !== harnessAuthSignIn || before?.loopback_peer !== result.loopback_peer ||
          JSON.stringify(before?.slots) !== JSON.stringify(result.slots) ||
          JSON.stringify(before?.harness_auth) !== JSON.stringify(result.harness_auth)) {
        render();
      }
    }

    return {
      element: group.root,
      render,
      applyStatus,
      closeEditors() {
        closeSlotAddEditor();
        cancelRename(false);
        render();
      },
      destroy() {
        if (slotPollTimer) { window.clearTimeout(slotPollTimer); slotPollTimer = null; }
        if (harnessAuthPollTimer) { window.clearTimeout(harnessAuthPollTimer); harnessAuthPollTimer = null; }
      }
    };
  }

  window.WsSettingsAccountHarness = {create};
}());
