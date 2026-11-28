/* Group 2 of Settings -> Account: Built-in Werk Runtime & API (OpenAI SIWC, OpenRouter, Local Endpoints). */
(function () {
  "use strict";

  function create(ctx) {
    const {
      el, button, append, svgIcon, t, h, nav, endpointSpec, endpointKey,
      accountRequest, pending, showTiers, openVoice, isVoiceOpenRouterShared, onStatusChange,
      refreshStatus, loadEndpoints, onChange
    } = ctx;

    const group = h.groupCard("2", t("js.settings.account.group_runtime_title"), t("js.settings.account.group_runtime_subtitle"));
    group.root.dataset.accountGroup = "runtime";

    let signIn = null, pollTimer = null;
    let openAIBusy = false, openAIError = "";
    let openRouterBusy = false, openRouterError = "", orForm = null;
    let endpointBusy = false, epForm = null;

    const openAI = h.providerBlock({
      rowName: "openai",
      iconClass: "ic-openai",
      monogram: "O",
      title: "OpenAI",
      tag: t("js.settings.account.openai_tag"),
      subtitle: t("js.settings.account.openai_description"),
      modelLink: "js.settings.account.openai_models",
      onModelLink: () => showTiers("openai")
    });

    const openRouter = h.providerBlock({
      rowName: "openrouter",
      iconClass: "ic-or",
      monogram: "R",
      title: "OpenRouter",
      tag: t("js.settings.account.api_key"),
      subtitle: t("js.settings.account.openrouter_description"),
      modelLink: "js.settings.account.openrouter_models",
      onModelLink: () => showTiers("openrouter")
    });

    const local = h.providerBlock({
      rowName: "local",
      iconClass: "ic-local",
      monogram: "L",
      title: t(endpointSpec.label),
      tag: endpointKey,
      subtitle: t(endpointSpec.description),
      modelLink: "js.settings.account.local_models",
      onModelLink: () => showTiers("local")
    });
    const endpointCountTag = el("span", "acc-tag");
    local.titleEl.append(" ", endpointCountTag);
    const endpointList = el("div", "account-endpoint-list");
    const endpointJson = el("details", "account-json");
    endpointJson.append(el("summary", "", t("js.settings.account.show_json")), el("pre"));
    local.kids.append(endpointList, append(el("div", "account-json-footer"), endpointJson));

    // Recessed OpenAI background_consent sub-setting row (mounted inside OpenAI's Level-2 stem).
    const consentRow = el("div", "acc-subset");
    consentRow.dataset.accountRow = "openai-consent";
    consentRow.tabIndex = -1;
    const consentTag = el("span", "acc-tag");
    const consentDesc = el("span", "acc-subset-desc", t("js.settings.account.consent_signin_required"));
    const consentToggle = button("tg", "", () => {
      const account = nav.status?.openai;
      if (!account?.client_id || openAIBusy) return;
      changeOpenAI("consent", {client_id: account.client_id, allowed: account.background_consent !== true});
    });
    consentToggle.setAttribute("role", "switch");
    consentToggle.setAttribute("aria-label", t("js.settings.account.consent_title"));
    consentToggle.setAttribute("aria-checked", "false");
    consentToggle.disabled = true;
    const consentWord = el("span", "tgl", t("js.settings.off"));
    consentRow.append(
      el("span", "acc-sl", t("js.settings.account.subsetting_label")),
      append(el("div", "acc-st"),
        append(el("div", "acc-st-head"), el("b", "", t("js.settings.account.consent_title")), consentTag),
        consentDesc),
      append(el("div", "acc-subset-ctl"), consentToggle, consentWord)
    );

    group.body.append(openAI.root, openRouter.root, local.root);

    function localKey(name) {
      return nav.status?.local_keys?.find(key => key.provider === "local/" + name);
    }

    /* ---------- OpenAI SIWC ---------- */
    function schedulePoll() {
      if (pollTimer || !pending(signIn)) return;
      pollTimer = window.setTimeout(pollSignIn, 900);
    }

    async function pollSignIn() {
      pollTimer = null;
      if (!pending(signIn)) return;
      try {
        const before = signIn;
        const result = await accountRequest("sign-in/status");
        if (before !== signIn) return;
        if (JSON.stringify(result) === JSON.stringify(before) && !openAIError) return;
        signIn = result;
        openAIError = "";
        updateOpenAI();
        if (result.state === "succeeded") {
          window.WsSettingsCombobox?.invalidateChoices();
          await refreshStatus();
        }
      } catch (_) {
        if (!openAIError) {
          openAIError = t("js.settings.account.progress_failed");
          updateOpenAI();
        }
      } finally {
        schedulePoll();
      }
    }

    async function changeOpenAI(path, body) {
      if (openAIBusy) return;
      openAIBusy = true;
      openAIError = "";
      updateOpenAI();
      try {
        const result = await accountRequest(path, body);
        if (path === "sign-out" || path === "openai/switch") window.WsSettingsCombobox?.invalidateChoices();
        if (result && path.startsWith("sign-in/")) {
          signIn = result;
          schedulePoll();
        }
      } catch (error) {
        openAIError = h.errorText(error.code);
      } finally {
        openAIBusy = false;
        if (path === "sign-out" || path === "consent" || path === "openai/switch") await refreshStatus();
        updateOpenAI();
      }
    }

    function updateConsent(account) {
      const linked = !!account?.client_id && !pending(signIn);
      if (!linked) {
        consentTag.textContent = "";
        consentTag.hidden = true;
        consentDesc.textContent = t("js.settings.account.consent_signin_required");
        consentToggle.setAttribute("aria-checked", "false");
        consentToggle.disabled = true;
        consentWord.textContent = t("js.settings.off");
        return;
      }
      const allowed = account.background_consent;
      const usable = !!nav.status?.openai_ready;
      const tagKey = allowed === true ? "js.settings.account.consent_allowed" :
        allowed === false ? "js.settings.account.consent_denied" : "js.settings.account.consent_unset";
      consentTag.textContent = t(tagKey);
      consentTag.hidden = false;
      consentDesc.textContent = t("js.settings.account.consent_description");
      consentToggle.setAttribute("aria-checked", String(allowed === true));
      consentToggle.disabled = openAIBusy || (!usable && allowed !== true);
      consentWord.textContent = t(allowed === true ? "js.settings.on" : "js.settings.off");
    }

    function updateOpenAI() {
      const account = nav.status?.openai;
      const rawAccounts = Array.isArray(nav.status?.openai_accounts) ? nav.status.openai_accounts : [];
      const accounts = rawAccounts.length ? rawAccounts : (account ? [Object.assign({active: true}, account)] : []);
      const savedFocus = h.captureFocus(openAI.kids);
      const existingPaste = openAI.kids.querySelector("[data-openai-paste]");
      const preservedPaste = existingPaste && (existingPaste.dataset.attempt || "") === (signIn?.attempt || "") ? existingPaste.value : "";

      openAI.root.classList.toggle("account-configured", !!nav.status?.openai_ready && !account?.plan_paused && !pending(signIn));
      openAI.acts.replaceChildren();
      const kidNodes = [];
      let notice = "", tone = "";

      if (pending(signIn)) {
        openAI.subEl.textContent = t("js.settings.account.browser_continue");
        openAI.acts.append(h.busyPill(signIn.state === "cancelling" ? "js.settings.account.cancelling" : "js.settings.account.signing_in"));
        openAI.acts.append(h.ab(t("js.settings.account.cancel"),
          () => changeOpenAI("sign-in/cancel", {attempt: signIn.attempt}),
          {disabled: signIn.state === "cancelling" || !signIn.attempt}));
        notice = t("js.settings.account.browser_continue");
        if (signIn.authorize_url && signIn.state !== "cancelling") {
          const drawer = el("div", "acc-drawer account-signin-drawer");
          if (signIn.browser_error) drawer.append(h.message(t("js.settings.account.signin_browser_fallback"), "warn"));
          const urlInput = el("input", "inp mono acc-inp");
          urlInput.id = "account-openai-auth-url";
          urlInput.readOnly = true;
          urlInput.value = signIn.authorize_url;
          urlInput.addEventListener("focus", () => { if (typeof urlInput.select === "function") urlInput.select(); });
          const copyBtn = h.ab(t("js.settings.account.copy_link"), () => h.copyText(signIn.authorize_url, copyBtn), {cls: "pri"});
          const openLink = el("a", "acc-ab", t("js.settings.account.open_link"));
          openLink.href = signIn.authorize_url;
          openLink.target = "_blank";
          openLink.rel = "noopener noreferrer";
          openLink.append(svgIcon("l-external-link"));
          drawer.append(append(el("div", "account-field"),
            el("label", "", t("js.settings.account.signin_url_label")),
            append(el("div", "account-copy-row"), urlInput, copyBtn, openLink)));

          const pasteForm = el("form", "account-form");
          pasteForm.autocomplete = "off";
          const pasteInput = el("input", "inp mono acc-inp");
          pasteInput.id = "account-openai-paste";
          pasteInput.dataset.openaiPaste = "1";
          pasteInput.dataset.attempt = signIn.attempt || "";
          pasteInput.spellcheck = false;
          pasteInput.autocomplete = "off";
          pasteInput.value = preservedPaste;
          const pasteHint = h.message(t(signIn.dynamic ? "js.settings.account.signin_paste_dynamic_help" : "js.settings.account.signin_paste_help"));
          const submit = h.ab(t("js.settings.account.signin_submit"), null, {cls: "solid", submit: true, disabled: openAIBusy});
          pasteForm.append(h.field(t("js.settings.account.signin_paste_label"), pasteInput, pasteHint),
            append(el("div", "account-actions"), submit));
          pasteForm.addEventListener("submit", event => {
            event.preventDefault();
            const val = pasteInput.value.trim();
            if (!val || openAIBusy) return;
            pasteInput.value = "";
            changeOpenAI("sign-in/complete", {attempt: signIn.attempt, input: val});
          });
          drawer.append(pasteForm);
          kidNodes.push(drawer);
        }
      } else if (nav.status) {
        let actionKey = "js.settings.account.sign_in";
        if (account) {
          if (!account.signed_in || (account.plan_enabled && !nav.status.openai_ready)) {
            actionKey = "js.settings.account.sign_in_again";
            notice = t("js.settings.account.reauth_help");
            tone = "fail";
          } else if (!account.plan_enabled) {
            actionKey = "js.settings.account.allow_plan";
            notice = t("js.settings.account.plan_disabled_help");
            tone = "warn";
          } else if (account.plan_paused) {
            notice = t("js.settings.account.plan_paused_help");
            tone = "warn";
          }
          openAI.subEl.textContent = t("js.settings.account.openai_summary", accounts.length, account.email || account.client_id);
        } else {
          openAI.subEl.textContent = t("js.settings.account.openai_description");
        }

        if (!nav.status.openai_ready || !account?.signed_in || !account.plan_enabled) {
          openAI.acts.append(h.ab(t(actionKey), () => {
            const body = {enable_plan: true};
            if (account?.client_id) body.client_id = account.client_id;
            changeOpenAI("sign-in/start", body);
          }, {cls: "pri", icon: "l-external-link", disabled: !nav.status.loopback_peer}));
        } else if (account.plan_paused) {
          const usage = el("a", "acc-ab pri", t("js.settings.account.review_usage"));
          usage.href = "https://chatgpt.com/settings/usage";
          usage.target = "_blank";
          usage.rel = "noopener noreferrer";
          usage.append(svgIcon("l-external-link"));
          openAI.acts.append(usage);
        } else {
          openAI.acts.append(h.ab(t("js.settings.account.add_account"),
            () => changeOpenAI("sign-in/start", {new_account: true, enable_plan: true}),
            {cls: "pri", disabled: !nav.status.loopback_peer}));
        }

        if (accounts.length) {
          for (const item of accounts) {
            const isActive = !!item.active || (accounts.length === 1 && item.client_id === account?.client_id);
            const row = el("div", "acc-row" + (isActive ? " is-active" : ""));
            const lead = el("div", "acc-lead");
            if (isActive) {
              lead.append(h.pill(t("js.settings.account.active"), "on", "", true));
            } else if (item.signed_in && item.plan_enabled) {
              lead.append(h.ab(t("js.settings.account.use_account"),
                () => changeOpenAI("openai/switch", {client_id: item.client_id}),
                {cls: "pri"}));
            } else {
              lead.append(h.pill(t("js.settings.account.reauth_required"), "fail"));
            }

            let stLabel = "js.settings.account.signed_in", stTone = "ok";
            if (!item.signed_in || (isActive && item.plan_enabled && !nav.status.openai_ready)) {
              stLabel = "js.settings.account.reauth_required";
              stTone = "fail";
            } else if (!item.plan_enabled) {
              stLabel = "js.settings.account.plan_disabled";
              stTone = "warn";
            } else if (item.plan_paused) {
              stLabel = "js.settings.account.plan_paused";
              stTone = "warn";
            }
            const main = append(el("div", "acc-row-main"),
              el("span", "acc-row-name", item.email || item.client_id),
              h.pill(t(stLabel), stTone, "", stTone === "ok"));
            if (stTone === "ok") {
              main.append(el("span", "acc-tag", t("js.settings.account.chatgpt_plan")));
            }
            const acts = el("div", "acc-row-act");
            if (!isActive && (!item.signed_in || !item.plan_enabled)) {
              acts.append(h.ab(t("js.settings.account.sign_in_again"),
                () => changeOpenAI("sign-in/start", {client_id: item.client_id, enable_plan: true}),
                {cls: "warn", disabled: !nav.status.loopback_peer}));
            }
            if (item.client_id) {
              acts.append(h.holdBar(t("js.settings.account.sign_out_btn"), t("js.settings.account.sign_out"),
                () => changeOpenAI("sign-out", {client_id: item.client_id})));
            }
            row.append(lead, main, acts);
            kidNodes.push(row);
          }
        } else {
          const empty = append(el("div", "acc-empty-row"),
            h.pill(t("js.settings.account.signed_out"), "unset"));
          kidNodes.push(empty);
        }
      }

      if (signIn?.state === "failed" && !pending(signIn)) {
        const reasons = {
          timed_out: "js.settings.account.timed_out",
          browser_unavailable: "js.settings.account.browser_unavailable"
        };
        notice = t(Object.hasOwn(reasons, signIn.reason) ? reasons[signIn.reason] : "js.settings.account.authorization_failed");
        tone = "fail";
      }
      if (signIn?.state === "cancelled") notice = t("js.settings.account.sign_in_cancelled");
      if (openAIError) {
        notice = openAIError;
        tone = "fail";
      }
      h.setNotice(openAI.noticeSlot, notice, tone);
      updateConsent(account);
      kidNodes.push(consentRow);
      openAI.kids.replaceChildren(...kidNodes);
      if (openAIBusy) openAI.acts.querySelectorAll("button").forEach(c => { c.disabled = true; });
      h.restoreFocus(openAI.kids, savedFocus);
    }

    /* ---------- OpenRouter API ---------- */
    function closeOpenRouterEditor() {
      if (!orForm) return;
      orForm.input.value = "";
      orForm.form.remove();
      orForm = null;
    }

    function showOpenRouterEditor() {
      if (orForm || openRouterBusy) return;
      openRouterError = "";
      const input = h.password("account-openrouter-key", "sk-or-v1-…");
      input.required = true;
      const hint = h.message("");
      const form = el("form", "acc-drawer account-or-form");
      form.autocomplete = "off";
      const save = h.ab(t("js.settings.save"), null, {cls: "solid", submit: true});
      const cancel = h.ab(t("js.settings.account.cancel"), () => {
        closeOpenRouterEditor();
        updateOpenRouter();
        openRouter.acts.querySelector("button")?.focus();
      });
      const error = h.message("", "fail");
      error.hidden = true;
      form.append(
        append(el("div", "acc-dr-t"), el("span", "", t("js.settings.account.openrouter_key"))),
        append(el("div", "account-form"),
          h.field(t(nav.status?.openrouter?.configured ? "js.settings.account.new_openrouter_key" : "js.settings.account.openrouter_key"), input, hint),
          append(el("div", "account-actions"), cancel, save)),
        error
      );
      const editor = {form, input, save, cancel, hint, error};
      function syncHint() {
        error.hidden = true;
        const unusual = input.value.length > 0 && !input.value.startsWith("sk-or-");
        hint.textContent = t(unusual ? "js.settings.account.prefix_warning" : "js.settings.account.key_help");
        hint.classList.toggle("warn", unusual);
        input.removeAttribute("aria-invalid");
      }
      input.addEventListener("input", syncHint);
      form.addEventListener("submit", event => {
        event.preventDefault();
        if (input.value && !openRouterBusy) changeOpenRouter("set", editor);
      });
      syncHint();
      orForm = editor;
      updateOpenRouter();
      input.focus();
    }

    async function changeOpenRouter(op, editor) {
      if (openRouterBusy) return;
      openRouterBusy = true;
      openRouterError = "";
      if (editor) {
        editor.input.disabled = editor.save.disabled = editor.cancel.disabled = true;
        (editor.save.querySelector("span") || editor.save).textContent = t("js.settings.account.checking");
        editor.error.hidden = true;
      }
      updateOpenRouter();
      try {
        const body = {op, provider: "openrouter"};
        if (editor) body.key = editor.input.value;
        const req = accountRequest("key", body);
        delete body.key;
        const result = await req;
        if (result.changed) window.WsSettingsCombobox?.invalidateChoices();
        if (editor && orForm === editor) closeOpenRouterEditor();
        nav.status.openrouter = result.status;
        if (typeof onStatusChange === "function") onStatusChange(nav.status);
        onChange();
      } catch (error) {
        openRouterError = h.errorText(error.code, editor ? "js.settings.account.key_retry" : "js.settings.account.request_failed");
        if (editor && orForm === editor) {
          editor.error.hidden = false;
          editor.error.textContent = openRouterError;
          if (["invalid_key", "key_rejected"].includes(error.code)) editor.input.setAttribute("aria-invalid", "true");
        }
      } finally {
        openRouterBusy = false;
        if (editor && orForm === editor) {
          editor.input.disabled = editor.save.disabled = editor.cancel.disabled = false;
          (editor.save.querySelector("span") || editor.save).textContent = t("js.settings.save");
          editor.input.focus();
        }
        updateOpenRouter();
        if (editor && !orForm && openRouter.root.isConnected && !openRouterError) {
          openRouter.root.focus({preventScroll: true});
        }
      }
    }

    function updateOpenRouter() {
      const current = nav.status?.openrouter;
      const savedFocus = h.captureFocus(openRouter.kids);
      openRouter.acts.replaceChildren();
      openRouter.acts.setAttribute("aria-busy", String(openRouterBusy));
      const state = current?.verification?.state || "unverified";
      openRouter.root.classList.toggle("account-configured", !!current?.configured && state === "verified");

      const kidNodes = [];
      let notice = "", tone = "";

      if (current?.configured) {
        const labelKey = state === "verified" ? "js.settings.account.key_set" :
          state === "rejected" ? "js.settings.account.rejected" : "js.settings.account.unverified";
        tone = state === "verified" ? "ok" : state === "rejected" ? "fail" : "warn";
        openRouter.subEl.textContent = t("js.settings.account.openrouter_description");

        if (!orForm) {
          openRouter.acts.append(h.ab(t("js.settings.account.replace_key"), showOpenRouterEditor, {cls: "pri"}));
          openRouter.acts.append(h.ab(t("js.settings.account.recheck"), () => changeOpenRouter("recheck")));
          openRouter.acts.append(h.holdBar(t("js.settings.account.remove_btn"), t("js.settings.account.remove_openrouter"),
            () => changeOpenRouter("remove")));
        }

        const badge = openRouterBusy
          ? h.busyPill("js.settings.account.checking")
          : h.pill(t(labelKey), tone, "", state === "verified");
        const checked = new Date(current.verification?.checked_at);
        if (!Number.isNaN(checked.valueOf())) {
          badge.title = t("js.settings.account.checked_at", checked.toLocaleString(document.documentElement.lang || document.documentElement.dataset.lang));
        }
        const row = el("div", "acc-row");
        const lead = append(el("div", "acc-lead"), h.pill(t("js.settings.account.shared_tag"), "on"));
        const main = append(el("div", "acc-row-main"),
          el("span", "acc-keypill", current.masked || "••••"),
          badge);
        if (isVoiceOpenRouterShared && isVoiceOpenRouterShared() && openVoice) {
          const chip = button("acc-chip-link", t("js.settings.account.openrouter_voice_chip"), () => openVoice());
          chip.dataset.accountVoiceChip = "1";
          main.append(chip);
        }
        row.append(lead, main);
        kidNodes.push(row);

        const reason = current.verification?.reason;
        const msgKey = state === "verified" ? "js.settings.account.key_verified" :
          state === "rejected" ? "js.settings.account.key_now_rejected" :
          reason === "network" ? "js.settings.account.key_network" :
          reason === "temporary" ? "js.settings.account.key_temporary" : "js.settings.account.key_unverified";
        notice = t(msgKey);
      } else if (nav.status) {
        openRouter.subEl.textContent = t("js.settings.account.openrouter_description");
        if (!orForm) {
          openRouter.acts.append(h.ab(t("js.settings.account.add_key"), showOpenRouterEditor, {cls: "pri"}));
        }
        kidNodes.push(append(el("div", "acc-empty-row"),
          h.pill(t("js.settings.account.unset"), "unset")));
      }

      if (orForm) kidNodes.push(orForm.form);
      openRouter.kids.replaceChildren(...kidNodes);
      if (openRouterBusy) openRouter.acts.querySelectorAll("button").forEach(c => { c.disabled = true; });
      if (openRouterError && !orForm) {
        notice = openRouterError;
        tone = "fail";
      }
      h.setNotice(openRouter.noticeSlot, notice, tone);
      h.restoreFocus(openRouter.kids, savedFocus);
    }

    /* ---------- Local Werk Endpoints ---------- */
    function updateEndpointMasks() {
      local.root.querySelectorAll("[data-endpoint-name]").forEach(row => {
        const name = row.dataset.endpointName;
        const key = localKey(name);
        const mask = row.querySelector(".account-endpoint-key");
        if (mask) {
          mask.classList.toggle("ok", !!key?.configured);
          mask.textContent = key?.configured ? t("js.settings.account.key_set") : t("js.settings.account.no_key");
          if (key?.configured && key.masked) mask.append(" ", el("span", "account-mask", key.masked));
        }
        const remove = row.querySelector(".account-endpoint-remove");
        if (remove) {
          const aria = t(key?.configured ? "js.settings.account.remove_endpoint_key" : "js.settings.account.remove_endpoint", name);
          remove.title = aria + " · " + t("js.settings.account.hold_hint");
          remove.setAttribute("aria-label", aria);
        }
      });
    }

    function syncEndpointControls() {
      const add = local.acts.querySelector("button");
      if (add) add.hidden = !!epForm;
      endpointList.querySelectorAll("[data-endpoint-name] button").forEach(control => {
        control.disabled = endpointBusy || !!epForm;
      });
    }

    function closeEndpointEditor() {
      if (!epForm) return;
      epForm.input.value = "";
      epForm.form.remove();
      epForm.row?.classList.remove("account-editing");
      epForm = null;
      syncEndpointControls();
    }

    function showEndpointEditor(name, value, row) {
      if (epForm || endpointBusy) return;
      const form = el("form", "acc-drawer account-ep-edit");
      form.autocomplete = "off";
      const id = el("input", "inp mono acc-inp");
      id.id = "account-endpoint-name";
      id.value = name || "";
      id.placeholder = "ollama-box";
      id.readOnly = !!name;
      id.spellcheck = false;
      id.required = true;
      id.maxLength = 64;
      const url = el("input", "inp mono acc-inp");
      url.id = "account-endpoint-url";
      url.value = value?.base_url || "";
      url.placeholder = "http://localhost:11434/v1";
      url.spellcheck = false;
      url.required = true;
      const input = h.password("account-endpoint-key", t("js.settings.account.optional_key_hint"));
      let kind = value?.kind || "ollama";
      const modes = el("div", "seg");
      modes.setAttribute("role", "group");
      modes.setAttribute("aria-label", t("js.settings.account.kind"));
      for (const mode of ["vllm", "ollama"]) {
        const node = button("", mode, () => {
          kind = mode;
          for (const option of modes.children) option.setAttribute("aria-pressed", String(option === node));
        });
        node.setAttribute("aria-pressed", String(kind === mode));
        modes.append(node);
      }
      const nameHint = h.message(t("js.settings.account.name_help"));
      const urlHint = h.message(t("js.settings.account.url_help"));
      const keyHint = h.message(t("js.settings.account.endpoint_key_help"));
      const save = h.ab(name ? t("js.settings.save") : t("js.settings.account.add"), null, {cls: "solid", submit: true});
      const cancel = h.ab(t("js.settings.account.cancel"), () => {
        closeEndpointEditor();
        (row?.querySelector("button") || local.acts.querySelector("button"))?.focus();
      });
      const error = h.message("", "fail");
      error.hidden = true;
      const warning = h.message("", "warn");
      warning.classList.add("inline-warn");
      warning.hidden = true;
      form.append(
        append(el("div", "account-ep-grid"),
          h.field(t("js.settings.account.name"), id, nameHint),
          h.field(t("js.settings.account.base_url"), url, urlHint),
          h.field(t("js.settings.account.kind"), modes),
          h.field(t("js.settings.account.optional_key"), input, keyHint)),
        warning, error,
        append(el("div", "acc-dr-foot"), cancel, save)
      );
      const editor = {name, row, input, form, save, error};
      const normalized = source => source.trim().replace(/\/+$/, "");
      function validate() {
        const validName = /^[a-z0-9][a-z0-9-]{0,63}$/.test(id.value);
        const validURL = /^https?:\/\/[^/?#@\s]+(?:\/[^?#\s]*)?$/i.test(url.value.trim()) && URL.canParse(url.value.trim());
        const duplicate = !name && nav.projection && Object.hasOwn(nav.projection, id.value);
        nameHint.textContent = t(duplicate ? "js.settings.account.name_taken" : validName || !id.value ? "js.settings.account.name_help" : "js.settings.account.invalid_name");
        urlHint.textContent = t(validURL || !url.value ? "js.settings.account.url_help" : "js.settings.account.invalid_url");
        const hasKey = !!name && localKey(name)?.configured;
        const urlChanged = !!name && normalized(url.value) !== normalized(value.base_url || "");
        warning.hidden = !hasKey || !urlChanged;
        warning.textContent = t("js.settings.account.url_retires_key");
        keyHint.textContent = t(urlChanged ? "js.settings.account.new_url_key_help" : hasKey ? "js.settings.account.keep_key_help" : "js.settings.account.endpoint_key_help");
        nameHint.classList.toggle("fail", !!id.value && (!validName || duplicate));
        urlHint.classList.toggle("fail", !!url.value && !validURL);
        id.setAttribute("aria-invalid", String(!!id.value && (!validName || duplicate)));
        url.setAttribute("aria-invalid", String(!!url.value && !validURL));
        save.disabled = !validName || !validURL || duplicate;
      }
      form.addEventListener("input", () => {
        error.hidden = true;
        validate();
      });
      form.addEventListener("submit", event => {
        event.preventDefault();
        validate();
        if (save.disabled || endpointBusy) return;
        const body = {op: "set", name: id.value, base_url: url.value.trim(), kind};
        if (input.value) body.key = input.value;
        changeEndpoint(body, editor);
      });
      epForm = editor;
      if (row) {
        row.classList.add("account-editing");
        row.after(form);
      } else {
        endpointList.append(form);
      }
      syncEndpointControls();
      validate();
      (name ? url : id).focus();
    }

    async function changeEndpoint(body, editor) {
      if (endpointBusy) return;
      endpointBusy = true;
      nav.endpointError = "";
      syncEndpointControls();
      if (editor) {
        editor.form.querySelectorAll("input, button").forEach(control => { control.disabled = true; });
        editor.error.hidden = true;
      }
      try {
        const req = accountRequest("endpoints", body);
        delete body.key;
        const result = await req;
        if (result.changed) window.WsSettingsCombobox?.invalidateChoices();
        if (editor && epForm === editor) closeEndpointEditor();
        nav.projection = Object.fromEntries(result.endpoints.map(({name, base_url, kind}) => [name, {base_url, kind}]));
        await Promise.all([loadEndpoints(), refreshStatus()]);
        if (local.root.isConnected) {
          const changed = Array.from(local.root.querySelectorAll("[data-endpoint-name]")).find(r => r.dataset.endpointName === body.name);
          (changed || local.root).focus({preventScroll: true});
        }
      } catch (error) {
        const notice = h.errorText(error.code);
        if (error.code === "endpoint_key_retired") await refreshStatus();
        if (editor && epForm === editor) {
          editor.error.hidden = false;
          editor.error.textContent = notice;
        } else {
          nav.endpointError = notice;
          updateEndpoints();
        }
      } finally {
        endpointBusy = false;
        if (editor && epForm === editor) editor.form.querySelectorAll("input, button").forEach(control => { control.disabled = false; });
        syncEndpointControls();
      }
    }

    function updateEndpoints() {
      const savedFocus = h.captureFocus(endpointList);
      const entries = Object.entries(nav.projection || {}).sort(([a], [b]) => a.localeCompare(b));
      endpointCountTag.textContent = nav.projection ? t("js.settings.account.endpoint_count", entries.length) : "";
      endpointCountTag.hidden = !nav.projection;
      endpointList.replaceChildren();
      local.acts.replaceChildren();
      endpointJson.hidden = !nav.projection;

      if (nav.endpointError) {
        const notice = h.message(nav.endpointError, "fail");
        notice.classList.add("account-endpoint-status");
        notice.append(" ", h.ab(t("js.settings.account.retry"), loadEndpoints, {cls: "pri"}));
        endpointList.append(notice);
      }
      if (!nav.projection) {
        if (!nav.endpointError) endpointList.append(el("p", "acc-empty-row", t("js.settings.loading")));
        return;
      }
      endpointJson.querySelector("pre").textContent = JSON.stringify(nav.projection, null, 2);
      local.acts.append(h.ab(t("js.settings.account.add_endpoint"), () => showEndpointEditor(), {cls: "pri"}));

      if (!entries.length && !epForm) {
        endpointList.append(el("div", "acc-empty-row", t("js.settings.account.endpoints_empty")));
      }
      for (const [name, value] of entries) {
        if (!value || typeof value !== "object") continue;
        const row = el("div", "acc-row account-ep-row");
        row.dataset.endpointName = name;
        row.tabIndex = -1;
        const lead = append(el("div", "acc-lead"), el("span", "acc-tag", value.kind));
        const urlEl = el("span", "acc-row-sub", value.base_url || "");
        urlEl.title = value.base_url || "";
        const keyBadge = el("span", "account-endpoint-key");
        const main = append(el("div", "acc-row-main"),
          el("span", "acc-row-name", name),
          el("code", "acc-tag", "local/" + name),
          keyBadge,
          urlEl);
        const edit = h.ab(t("js.settings.account.edit_btn"), () => showEndpointEditor(name, value, row), {
          ariaLabel: t("js.settings.account.edit_endpoint", name),
          title: t("js.settings.account.edit_endpoint", name)
        });
        const remove = h.holdBar(t("js.settings.account.remove_btn"), t("js.settings.account.remove_endpoint", name),
          () => changeEndpoint({op: "remove", name}));
        remove.classList.add("account-endpoint-remove");
        row.append(lead, main, append(el("div", "acc-row-act"), edit, remove));
        endpointList.append(row);
        if (epForm?.name === name) {
          epForm.row = row;
          row.classList.add("account-editing");
          endpointList.append(epForm.form);
        }
      }
      if (epForm && !epForm.name) endpointList.append(epForm.form);
      updateEndpointMasks();
      syncEndpointControls();
      h.restoreFocus(endpointList, savedFocus);
    }

    function applyStatus(before, result) {
      const previousAttempt = signIn;
      if (!openAIBusy && JSON.stringify(signIn) !== JSON.stringify(result.sign_in)) signIn = result.sign_in;
      if (pending(signIn)) schedulePoll();
      if (JSON.stringify(before?.openrouter) !== JSON.stringify(result.openrouter)) updateOpenRouter();
      if (before?.openai_ready !== result.openai_ready || before?.loopback_peer !== result.loopback_peer ||
          previousAttempt !== signIn || JSON.stringify(before?.openai) !== JSON.stringify(result.openai) ||
          JSON.stringify(before?.openai_accounts) !== JSON.stringify(result.openai_accounts)) {
        updateOpenAI();
      }
      if (JSON.stringify(before?.local_keys) !== JSON.stringify(result.local_keys)) updateEndpointMasks();
    }

    function focus(provider) {
      const isEndpoint = provider.startsWith("local/");
      const name = isEndpoint ? provider.slice(6) : provider;
      const node = isEndpoint
        ? Array.from(local.root.querySelectorAll("[data-endpoint-name]")).find(r => r.dataset.endpointName === name) || local.root
        : (provider === "openai" ? openAI.root : provider === "openrouter" ? openRouter.root : local.root);
      node.focus({preventScroll: true});
      node.scrollIntoView({block: "nearest"});
    }

    updateOpenAI();
    updateEndpoints();

    return {
      element: group.root,
      applyStatus,
      updateOpenRouter,
      updateOpenAI,
      updateEndpoints,
      updateEndpointMasks,
      closeEditors() {
        closeOpenRouterEditor();
        updateOpenRouter();
        closeEndpointEditor();
      },
      focus,
      destroy() {
        if (pollTimer) { window.clearTimeout(pollTimer); pollTimer = null; }
      }
    };
  }

  window.WsSettingsAccountRuntime = {create};
}());
