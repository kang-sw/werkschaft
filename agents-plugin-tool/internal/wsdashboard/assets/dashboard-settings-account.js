/* Machine-wide provider settings. Only password inputs hold unsaved keys. */
(function () {
  "use strict";

  const endpointKey = "agent.werk_endpoints";
  const pending = attempt => attempt && ["starting", "waiting", "cancelling"].includes(attempt.state);

  async function accountRequest(path, body) {
    const options = {credentials: "same-origin", cache: "no-store"};
    if (body) Object.assign(options, {
      method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify(body)
    });
    const response = await fetch("/api/account/" + path, options);
    if (response.status === 204) return;
    const result = await response.json().catch(() => ({}));
    if (response.ok) return result;
    throw Object.assign(new Error(), {code: result.error});
  }

  function create({el, button, append, svgIcon, request, showTiers, onChange, endpointSpec}) {
    const t = window.wsT;
    const btn = (label, action, primary) => button("btn" + (primary ? " pri" : ""), label, action);
    const link = (label, action) => append(button("account-link", label, action), svgIcon("l-chevron-right"));
    const section = key => append(el("div", "sec-head"), el("h3", "", t(key)));
    const panel = el("div", "account-panel");
    const providers = el("div", "sgroup");
    const slotsHead = section("js.settings.account.slots_heading");
    const slotsGroup = el("div", "sgroup");
    const voiceHost = el("div", "account-voice-host");
    const endpoints = el("div", "sgroup");
    const providerNotice = el("p", "info-note");
    const nav = {status: null, projection: null, isLoaded: false, error: false};
    let statusLoad, endpointLoad, signIn, pollTimer, slotSignIn, slotPollTimer, harnessAuthSignIn, harnessAuthPollTimer;
    let openRouterBusy = false, openAIBusy = false, endpointBusy = false, slotBusy = false, harnessAuthBusy = false;
    let orForm, epForm, slotAddForm, openRouterRow, openAIRow, consentRow, endpointRow, voiceSection;
    let openRouterError = "", openAIError = "", endpointError = "", slotError = "", harnessAuthError = "";

    providerNotice.hidden = true;
    providerNotice.setAttribute("role", "status");
    slotsHead.hidden = true;
    slotsGroup.hidden = true;
    panel.append(el("p", "info-note", t("js.settings.account.machine_note")), providerNotice,
      section("js.settings.account.providers"), providers,
      slotsHead, slotsGroup,
      voiceHost,
      section("js.settings.account.endpoints"), endpoints);

    function errorText(code, fallback = "js.settings.account.request_failed") {
      const errors = {
        invalid_key: "js.settings.account.invalid_key",
        key_rejected: "js.settings.account.key_rejected",
        invalid_endpoint: "js.settings.account.invalid_endpoint",
        endpoint_key_retired: "js.settings.account.endpoint_key_retired",
        credential_changed: "js.settings.account.credential_changed",
        sign_in_state_mismatch: "js.settings.account.signin_state_mismatch",
        sign_in_client_id_missing: "js.settings.account.signin_client_id_missing",
        sign_in_invalid_callback: "js.settings.account.signin_invalid_callback",
        sign_in_already_completed: "js.settings.account.signin_already_completed",
        sign_in_changed: "js.settings.account.signin_changed",
        callback_url_rejected: "js.settings.account.slot_callback_rejected",
        callback_host_not_loopback: "js.settings.account.slot_callback_rejected",
        invalid_callback_url: "js.settings.account.slot_callback_rejected",
        callback_port_mismatch: "js.settings.account.slot_callback_port_mismatch",
        callback_not_ready: "js.settings.account.slot_callback_rejected",
        callback_replay_failed: "js.settings.account.slot_login_failed",
        invalid_auth_code: "js.settings.account.invalid_auth_code",
        stdin_not_ready: "js.settings.account.slot_login_failed",
        stdin_write_failed: "js.settings.account.slot_login_failed",
        slot_exists: "js.settings.account.slot_exists",
        config_dir_is_default: "js.settings.account.slot_dir_default",
        config_dir_not_found: "js.settings.account.slot_dir_missing",
        invalid_config_dir: "js.settings.account.slot_dir_missing",
        cli_unavailable: "js.settings.account.slot_cli_unavailable",
        auth_preflight_failed: "js.settings.account.slot_preflight_failed",
        auth_not_ready: "js.settings.account.slot_preflight_failed",
        login_failed: "js.settings.account.slot_login_failed",
        timed_out: "js.settings.account.timed_out"
      };
      return t(Object.hasOwn(errors, code) ? errors[code] : fallback);
    }

    function copyText(text, control) {
      if (!text) return;
      const done = () => {
        if (!control) return;
        const prev = control.textContent;
        control.textContent = t("js.settings.account.copied");
        window.setTimeout(() => { if (control.isConnected) control.textContent = prev; }, 1400);
      };
      if (navigator.clipboard && typeof navigator.clipboard.writeText === "function") {
        navigator.clipboard.writeText(text).then(done).catch(() => {});
      }
    }

    function localKey(name) {
      return nav.status?.local_keys?.find(key => key.provider === "local/" + name);
    }

    function attention() {
      if (!nav.status) return 0;
      const {openrouter, openai, openai_ready, consent_prompt} = nav.status;
      return Number(!openrouter?.configured || openrouter.verification?.state === "rejected") +
        Number(!openai_ready || !!openai?.plan_paused) + Number(consent_prompt > 0);
    }

    function providerIssue(provider) {
      if (provider.startsWith("local/")) {
        const name = provider.slice(6);
        return nav.projection && !Object.hasOwn(nav.projection, name) ? t("js.settings.account.endpoint_missing", name) : "";
      }
      if (!nav.status) return "";
      if (provider === "openrouter") {
        if (!nav.status.openrouter?.configured) return t("js.settings.account.openrouter_missing");
        if (nav.status.openrouter.verification?.state === "rejected") return t("js.settings.account.openrouter_rejected");
      }
      if (provider === "openai") {
        const account = nav.status.openai;
        if (!account) return t("js.settings.account.openai_missing");
        if (!account.signed_in) return t("js.settings.account.reauth_required");
        if (!account.plan_enabled) return t("js.settings.account.plan_disabled");
        if (!nav.status.openai_ready) return t("js.settings.account.reauth_required");
      }
      return "";
    }

    function refreshStatus() {
      if (statusLoad) return statusLoad;
      statusLoad = accountRequest("status").then(result => {
        const before = nav.status;
        const previousAttempt = signIn;
        const previousSlotAttempt = slotSignIn;
        const previousHarnessAuth = harnessAuthSignIn;
        nav.status = result;
        nav.error = false;
        if (!openAIBusy && JSON.stringify(signIn) !== JSON.stringify(result.sign_in)) signIn = result.sign_in;
        if (pending(signIn)) schedulePoll();
        if (!slotBusy && result.slot_sign_in && JSON.stringify(slotSignIn) !== JSON.stringify(result.slot_sign_in)) slotSignIn = result.slot_sign_in;
        if (pending(slotSignIn)) scheduleSlotPoll();
        const activeHarnessAuth = Array.isArray(result.harness_auth) ? result.harness_auth.find(item => pending(item.sign_in))?.sign_in || result.harness_auth.find(item => item.sign_in && item.sign_in.state && item.sign_in.state !== "idle")?.sign_in : null;
        const nextHarnessAuth = activeHarnessAuth || { state: "idle" };
        if (!harnessAuthBusy && JSON.stringify(harnessAuthSignIn) !== JSON.stringify(nextHarnessAuth)) harnessAuthSignIn = nextHarnessAuth;
        if (pending(harnessAuthSignIn)) scheduleHarnessAuthPoll();
        if (JSON.stringify(before?.openrouter) !== JSON.stringify(result.openrouter)) updateOpenRouter();
        if (before?.openai_ready !== result.openai_ready || before?.loopback_peer !== result.loopback_peer ||
            previousAttempt !== signIn || JSON.stringify(before?.openai) !== JSON.stringify(result.openai) ||
            JSON.stringify(before?.openai_accounts) !== JSON.stringify(result.openai_accounts)) updateOpenAI();
        if (previousSlotAttempt !== slotSignIn || previousHarnessAuth !== harnessAuthSignIn || before?.loopback_peer !== result.loopback_peer ||
            JSON.stringify(before?.slots) !== JSON.stringify(result.slots) ||
            JSON.stringify(before?.harness_auth) !== JSON.stringify(result.harness_auth)) updateSlots();
        if (JSON.stringify(before?.local_keys) !== JSON.stringify(result.local_keys)) updateEndpointMasks();
        if (voiceSection && (JSON.stringify(before?.voice_keys) !== JSON.stringify(result.voice_keys) ||
            JSON.stringify(before?.openrouter) !== JSON.stringify(result.openrouter))) voiceSection.render();
      }).catch(() => { nav.error = true; }).finally(() => {
        statusLoad = null;
        providerNotice.replaceChildren();
        providerNotice.hidden = !nav.error;
        if (nav.error) providerNotice.append(t("js.settings.account.status_failed"), " ", btn(t("js.settings.account.retry"), refreshStatus));
        onChange();
      });
      return statusLoad;
    }

    function loadEndpoints() {
      if (endpointLoad) return endpointLoad;
      endpointLoad = request("/api/config/snapshot?scope=global").then(config => {
        if (voiceSection) voiceSection.applyConfigSnapshot(config);
        const entry = config.entries.find(item => item.key === endpointKey);
        if (!entry) throw new Error();
        const value = typeof entry.value === "string" ? JSON.parse(entry.value.trim() || "{}") : entry.value;
        nav.projection = value && typeof value === "object" && !Array.isArray(value) ? value : {};
        endpointError = "";
      }).catch(() => {
        endpointError = t("js.settings.account.endpoints_failed");
      }).finally(() => {
        nav.isLoaded = true;
        endpointLoad = null;
        updateEndpoints();
        if (voiceSection) voiceSection.render();
        onChange();
      });
      return endpointLoad;
    }

    function hold(iconName, label, action) {
      const node = iconButton(iconName, label);
      window.WsHoldConfirm.attach(node, {mode: "radial", onConfirm: action});
      return node;
    }

    function iconButton(iconName, label, action) {
      const node = append(button("reset-icon-btn account-icon", "", action), svgIcon(iconName));
      node.title = label;
      node.setAttribute("aria-label", label);
      return node;
    }

    function pill(label, tone, detail, icon) {
      const node = el("span", "origin-pill" + (tone ? " " + tone : ""));
      if (icon) {
        const check = el("span", "", "✓");
        check.setAttribute("aria-hidden", "true");
        node.append(check);
      }
      node.append(el("span", "lbl", label));
      if (detail) node.append(el("span", "account-mask", detail));
      return node;
    }

    function message(text, tone = "") {
      const node = el("p", "hint account-message" + (tone ? " " + tone : ""), text);
      if (tone === "fail") node.setAttribute("role", "alert");
      return node;
    }

    function meta(label, description, {tag, key, provider, modelLink}) {
      const line = append(el("div", "klabel-line"), el("span", "klabel", label));
      if (tag) line.append(el("span", "tag", tag));
      if (key) line.append(el("code", "skey", key));
      return append(el("div", "kmeta"), line, el("p", "kdesc", description),
        link(t(modelLink), () => showTiers(provider)));
    }

    function accountRow(name) {
      const row = el("div", "srow account-row");
      row.dataset.accountRow = name;
      row.tabIndex = -1;
      return row;
    }

    function busyPill(label, detail) {
      return pill(t(label), "busy", detail);
    }

    function password(id, placeholder) {
      const input = el("input", "inp mono");
      input.id = id;
      input.type = "password";
      input.autocomplete = "off";
      input.spellcheck = false;
      if (placeholder) input.placeholder = placeholder;
      return input;
    }

    function field(label, input, hint) {
      const title = el("label", "", label);
      if (input.id) title.htmlFor = input.id;
      const node = append(el("div", "account-field"), title, input);
      if (hint) {
        if (input.id) {
          hint.id = input.id + "-hint";
          input.setAttribute("aria-describedby", hint.id);
        }
        node.append(hint);
      }
      return node;
    }

    function closeOpenRouterEditor() {
      if (!orForm) return;
      orForm.input.value = "";
      orForm.form.remove();
      orForm = null;
    }

    function updateOpenRouter() {
      const current = nav.status?.openrouter;
      const info = openRouterRow.querySelector(".kmeta");
      const rail = openRouterRow.querySelector(".account-rail");
      info.querySelector(".account-message")?.remove();
      rail.replaceChildren();
      rail.setAttribute("aria-busy", String(openRouterBusy));
      const state = current?.verification?.state || "unverified";
      openRouterRow.classList.toggle("account-configured", !!current?.configured && state === "verified");
      if (current?.configured) {
        const label = state === "verified" ? "js.settings.account.key_set" :
          state === "rejected" ? "js.settings.account.rejected" : "js.settings.account.unverified";
        const tone = state === "verified" ? "ok" : state === "rejected" ? "fail" : "warn";
        const badge = openRouterBusy ? busyPill("js.settings.account.checking", current.masked) : pill(t(label), tone, current.masked, state === "verified");
        const checked = new Date(current.verification?.checked_at);
        if (!Number.isNaN(checked.valueOf())) badge.title = t("js.settings.account.checked_at", checked.toLocaleString(document.documentElement.lang || document.documentElement.dataset.lang));
        rail.append(badge);
        if (!orForm && state !== "verified") rail.append(btn(t("js.settings.account.recheck"), () => changeOpenRouter("recheck")));
        if (!orForm) rail.append(btn(t("js.settings.account.replace_key"), showOpenRouterEditor),
          hold("i-trash", t("js.settings.account.remove_openrouter"), () => changeOpenRouter("remove")));
        const reason = current.verification?.reason;
        const text = state === "verified" ? "js.settings.account.key_verified" :
          state === "rejected" ? "js.settings.account.key_now_rejected" :
          reason === "network" ? "js.settings.account.key_network" :
          reason === "temporary" ? "js.settings.account.key_temporary" : "js.settings.account.key_unverified";
        info.append(message(t(text), tone));
      } else if (nav.status) {
        rail.append(pill(t("js.settings.account.unset"), "unset"));
        if (!orForm) rail.append(btn(t("js.settings.account.add_key"), showOpenRouterEditor));
      }
      if (openRouterBusy) rail.querySelectorAll("button").forEach(control => { control.disabled = true; });
      if (openRouterError && !orForm) {
        info.querySelector(".account-message")?.remove();
        info.append(message(openRouterError, "fail"));
      }
    }

    function showOpenRouterEditor() {
      if (orForm || openRouterBusy) return;
      openRouterError = "";
      const input = password("account-openrouter-key", "sk-or-v1-…");
      input.required = true;
      const hint = message("");
      const form = el("form", "account-drawer account-or-form");
      form.autocomplete = "off";
      const save = btn(t("js.settings.save"), null, true);
      save.type = "submit";
      const cancel = btn(t("js.settings.account.cancel"), () => {
        closeOpenRouterEditor();
        updateOpenRouter();
        openRouterRow.querySelector(".account-rail button:last-of-type")?.focus();
      });
      const error = message("", "fail");
      error.hidden = true;
      form.append(append(el("div", "account-form"),
        field(t(nav.status?.openrouter?.configured ? "js.settings.account.new_openrouter_key" : "js.settings.account.openrouter_key"), input, hint),
        append(el("div", "account-actions"), save, cancel)), error);
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
      openRouterRow.append(form);
      updateOpenRouter();
      input.focus();
    }

    async function changeOpenRouter(op, editor) {
      if (openRouterBusy) return;
      openRouterBusy = true;
      openRouterError = "";
      if (editor) {
        editor.input.disabled = editor.save.disabled = editor.cancel.disabled = true;
        editor.save.textContent = t("js.settings.account.checking");
        editor.error.hidden = true;
      }
      updateOpenRouter();
      try {
        const body = {op, provider: "openrouter"};
        if (editor) body.key = editor.input.value;
        const request = accountRequest("key", body);
        delete body.key;
        const result = await request;
        if (result.changed) window.WsSettingsCombobox?.invalidateChoices();
        if (editor && orForm === editor) closeOpenRouterEditor();
        nav.status.openrouter = result.status;
        onChange();
      } catch (error) {
        openRouterError = errorText(error.code, editor ? "js.settings.account.key_retry" : "js.settings.account.request_failed");
        if (editor && orForm === editor) {
          editor.error.hidden = false;
          editor.error.textContent = openRouterError;
          if (["invalid_key", "key_rejected"].includes(error.code)) editor.input.setAttribute("aria-invalid", "true");
        }
      } finally {
        openRouterBusy = false;
        if (editor && orForm === editor) {
          editor.input.disabled = editor.save.disabled = editor.cancel.disabled = false;
          editor.save.textContent = t("js.settings.save");
          editor.input.focus();
        }
        updateOpenRouter();
        if (editor && !orForm && openRouterRow.isConnected && !openRouterError) openRouterRow.focus({preventScroll: true});
      }
    }

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
        if (result && (path.startsWith("sign-in/"))) {
          signIn = result;
          schedulePoll();
        }
      } catch (error) {
        openAIError = errorText(error.code);
      } finally {
        openAIBusy = false;
        if (path === "sign-out" || path === "consent" || path === "openai/switch") await refreshStatus();
        updateOpenAI();
      }
    }

    function updateOpenAI() {
      const account = nav.status?.openai;
      const accounts = Array.isArray(nav.status?.openai_accounts) ? nav.status.openai_accounts : [];
      const rail = openAIRow.querySelector(".account-rail");
      const info = openAIRow.querySelector(".kmeta");
      const existingPaste = openAIRow.querySelector("[data-openai-paste]");
      const preservedAttempt = existingPaste?.dataset.attempt || "";
      const preservedPaste = existingPaste && preservedAttempt === (signIn?.attempt || "") ? existingPaste.value : "";
      openAIRow.classList.toggle("account-configured", !!nav.status?.openai_ready && !account?.plan_paused && !pending(signIn));
      info.querySelector(".account-message")?.remove();
      openAIRow.querySelector(".account-signin-drawer")?.remove();
      openAIRow.querySelector(".account-sublist")?.remove();
      rail.replaceChildren();
      let notice, tone;
      if (pending(signIn)) {
        rail.append(busyPill(signIn.state === "cancelling" ? "js.settings.account.cancelling" : "js.settings.account.signing_in"));
        const cancel = btn(t("js.settings.account.cancel"), () => changeOpenAI("sign-in/cancel", {attempt: signIn.attempt}));
        cancel.disabled = signIn.state === "cancelling" || !signIn.attempt;
        rail.append(cancel);
        notice = t("js.settings.account.browser_continue");
        if (signIn.authorize_url && signIn.state !== "cancelling") {
          const drawer = el("div", "account-drawer account-signin-drawer");
          if (signIn.browser_error) drawer.append(message(t("js.settings.account.signin_browser_fallback"), "warn"));
          const urlInput = el("input", "inp mono");
          urlInput.id = "account-openai-auth-url";
          urlInput.readOnly = true;
          urlInput.value = signIn.authorize_url;
          urlInput.addEventListener("focus", () => urlInput.select());
          const copyBtn = btn(t("js.settings.account.copy_link"), () => copyText(signIn.authorize_url, copyBtn));
          const openLink = el("a", "btn", t("js.settings.account.open_link"));
          openLink.href = signIn.authorize_url;
          openLink.target = "_blank";
          openLink.rel = "noopener noreferrer";
          const urlRow = append(el("div", "account-copy-row"), urlInput, copyBtn, append(openLink, svgIcon("l-external-link")));
          drawer.append(append(el("div", "account-field"), el("label", "", t("js.settings.account.signin_url_label")), urlRow));

          const pasteForm = el("form", "account-form");
          pasteForm.autocomplete = "off";
          const pasteInput = el("input", "inp mono");
          pasteInput.id = "account-openai-paste";
          pasteInput.dataset.openaiPaste = "1";
          pasteInput.dataset.attempt = signIn.attempt || "";
          pasteInput.spellcheck = false;
          pasteInput.autocomplete = "off";
          pasteInput.value = preservedPaste;
          const pasteHint = message(t(signIn.dynamic ? "js.settings.account.signin_paste_dynamic_help" : "js.settings.account.signin_paste_help"));
          const submit = btn(t("js.settings.account.signin_submit"), null, true);
          submit.type = "submit";
          submit.disabled = openAIBusy;
          pasteForm.append(field(t("js.settings.account.signin_paste_label"), pasteInput, pasteHint),
            append(el("div", "account-actions"), submit));
          pasteForm.addEventListener("submit", event => {
            event.preventDefault();
            const val = pasteInput.value.trim();
            if (!val || openAIBusy) return;
            pasteInput.value = "";
            changeOpenAI("sign-in/complete", {attempt: signIn.attempt, input: val});
          });
          drawer.append(pasteForm);
          openAIRow.append(drawer);
        }
      } else if (nav.status) {
        let label = "js.settings.account.signed_out", action = "js.settings.account.sign_in", badge = "unset";
        if (account) {
          label = "js.settings.account.signed_in";
          badge = "ok";
          if (!account.signed_in || (account.plan_enabled && !nav.status.openai_ready)) {
            label = "js.settings.account.reauth_required";
            action = "js.settings.account.sign_in_again";
            badge = "fail";
            notice = t("js.settings.account.reauth_help");
            tone = "fail";
          } else if (!account.plan_enabled) {
            label = "js.settings.account.plan_disabled";
            action = "js.settings.account.allow_plan";
            badge = "warn";
            notice = t("js.settings.account.plan_disabled_help");
            tone = "warn";
          } else if (account.plan_paused) {
            label = "js.settings.account.plan_paused";
            badge = "warn";
            notice = t("js.settings.account.plan_paused_help");
            tone = "warn";
          } else {
            notice = t("js.settings.account.chatgpt_plan");
          }
        }
        rail.append(pill(t(label), badge, account?.email));
        if (!nav.status.openai_ready || !account?.signed_in || !account.plan_enabled) {
          const sign = btn(t(action), () => {
            const body = {enable_plan: true};
            if (account?.client_id) body.client_id = account.client_id;
            changeOpenAI("sign-in/start", body);
          }, true);
          sign.disabled = !nav.status.loopback_peer;
          rail.append(append(sign, svgIcon("l-external-link")));
        } else if (account.plan_paused) {
          const usage = el("a", "btn", t("js.settings.account.review_usage"));
          usage.href = "https://chatgpt.com/settings/usage";
          usage.target = "_blank";
          usage.rel = "noopener noreferrer";
          rail.append(append(usage, svgIcon("l-external-link")));
        } else {
          const addAnother = btn(t("js.settings.account.add_account"), () => changeOpenAI("sign-in/start", {new_account: true, enable_plan: true}));
          addAnother.disabled = !nav.status.loopback_peer;
          rail.append(addAnother);
        }
        if (account?.client_id) rail.append(hold("i-power", t("js.settings.account.sign_out"), () => changeOpenAI("sign-out", {client_id: account.client_id})));

        if (accounts.length > 1) {
          const sublist = el("div", "account-sublist");
          for (const item of accounts) {
            const rowItem = el("div", "account-subitem" + (item.active ? " active" : ""));
            const statusTone = item.active ? "ok" : (!item.signed_in ? "fail" : !item.plan_enabled ? "warn" : "");
            const statusLabel = item.active ? "js.settings.account.active" :
              !item.signed_in ? "js.settings.account.reauth_required" :
              !item.plan_enabled ? "js.settings.account.plan_disabled" : "js.settings.account.signed_in";
            const itemMeta = append(el("div", "account-subitem-meta"),
              el("span", "account-slot-label", item.email || item.client_id),
              pill(t(statusLabel), statusTone, "", item.active));
            const actions = el("div", "account-subitem-actions");
            if (!item.active && item.signed_in && item.plan_enabled) {
              actions.append(btn(t("js.settings.account.use_account"), () => changeOpenAI("openai/switch", {client_id: item.client_id})));
            } else if (!item.active) {
              const resign = btn(t("js.settings.account.sign_in_again"), () => changeOpenAI("sign-in/start", {client_id: item.client_id, enable_plan: true}));
              resign.disabled = !nav.status.loopback_peer;
              actions.append(resign);
            }
            actions.append(hold("i-power", t("js.settings.account.sign_out"), () => changeOpenAI("sign-out", {client_id: item.client_id})));
            rowItem.append(itemMeta, actions);
            sublist.append(rowItem);
          }
          openAIRow.append(sublist);
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
      if (notice) info.append(message(notice, tone));
      if (openAIBusy) rail.querySelectorAll("button").forEach(control => { control.disabled = true; });
      updateConsent(account);
    }

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
        updateSlots();
        if (result.state === "succeeded") {
          await refreshStatus();
        }
      } catch (_) {
        if (!slotError) {
          slotError = t("js.settings.account.progress_failed");
          updateSlots();
        }
      } finally {
        scheduleSlotPoll();
      }
    }

    async function changeSlot(path, body) {
      if (slotBusy) return;
      slotBusy = true;
      slotError = "";
      updateSlots();
      try {
        const result = await accountRequest(path, body);
        if (path.startsWith("slots/sign-in/")) {
          slotSignIn = result;
          scheduleSlotPoll();
        }
        if (path === "slots/add") closeSlotAddEditor();
      } catch (error) {
        slotError = errorText(error.code);
      } finally {
        slotBusy = false;
        if (path === "slots/switch" || path === "slots/add" || path === "slots/remove") await refreshStatus();
        updateSlots();
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
      const form = el("form", "account-drawer");
      form.autocomplete = "off";
      const labelInp = el("input", "inp");
      labelInp.id = "account-slot-label-" + harness;
      labelInp.placeholder = t("js.settings.account.slot_label_placeholder");
      labelInp.maxLength = 80;
      const dirInp = el("input", "inp mono");
      dirInp.id = "account-slot-dir-" + harness;
      dirInp.placeholder = t("js.settings.account.slot_dir_placeholder");
      dirInp.spellcheck = false;
      dirInp.required = true;
      const dirHint = message(t("js.settings.account.slot_dir_help"));
      const save = btn(t("js.settings.account.add"), null, true);
      save.type = "submit";
      const cancel = btn(t("js.settings.account.cancel"), () => { closeSlotAddEditor(); updateSlots(); });
      form.append(append(el("div", "account-slot-form-grid"),
        field(t("js.settings.account.slot_label"), labelInp),
        field(t("js.settings.account.slot_dir_label"), dirInp, dirHint)),
        append(el("div", "account-ep-foot"), save, cancel));
      form.addEventListener("submit", event => {
        event.preventDefault();
        const dir = dirInp.value.trim();
        if (!dir || slotBusy) return;
        changeSlot("slots/add", {harness, label: labelInp.value.trim(), config_dir: dir});
      });
      slotAddForm = {harness, form, labelInp, dirInp};
      updateSlots();
      labelInp.focus();
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
        updateSlots();
        if (nextSignIn?.state === "succeeded") await refreshStatus();
      } catch (_) {
        if (!harnessAuthError) {
          harnessAuthError = t("js.settings.account.progress_failed");
          updateSlots();
        }
      } finally {
        scheduleHarnessAuthPoll();
      }
    }

    async function changeHarnessAuth(path, body) {
      if (harnessAuthBusy) return;
      harnessAuthBusy = true;
      harnessAuthError = "";
      updateSlots();
      try {
        const result = await accountRequest(path, body);
        if (path === "harness-auth/reverify") {
          await refreshStatus();
        } else if (result) {
          harnessAuthSignIn = result;
          scheduleHarnessAuthPoll();
        }
      } catch (error) {
        harnessAuthError = errorText(error.code);
      } finally {
        harnessAuthBusy = false;
        updateSlots();
      }
    }

    function updateSlots() {
      const slotsMap = nav.status?.slots;
      const modesMap = nav.status?.slot_login_modes || {};
      const harnesses = slotsMap && typeof slotsMap === "object" ? Object.keys(slotsMap).sort() : [];
      const singleAuths = Array.isArray(nav.status?.harness_auth)
        ? nav.status.harness_auth.filter(item => item && (item.available || item.probed || item.auth_required || pending(item.sign_in)))
        : [];
      const anyVisible = harnesses.length > 0 || singleAuths.length > 0;
      slotsHead.hidden = !anyVisible;
      slotsGroup.hidden = !anyVisible;
      if (!anyVisible) {
        slotsGroup.replaceChildren();
        return;
      }
      const existingPaste = slotsGroup.querySelector("[data-slot-paste]");
      const preservedAttempt = existingPaste?.dataset.attempt || "";
      const preservedPaste = existingPaste && preservedAttempt === (slotSignIn?.attempt || "") ? existingPaste.value : "";
      const existingAuthPaste = slotsGroup.querySelector("[data-harness-auth-paste]");
      const preservedAuthAttempt = existingAuthPaste?.dataset.attempt || "";
      const preservedAuthPaste = existingAuthPaste && preservedAuthAttempt === (harnessAuthSignIn?.attempt || "") ? existingAuthPaste.value : "";
      slotsGroup.replaceChildren();
      for (const harness of harnesses) {
        const list = Array.isArray(slotsMap[harness]) ? slotsMap[harness] : [];
        const modes = Array.isArray(modesMap[harness]) && modesMap[harness].length ? modesMap[harness] : ["browser"];
        const row = accountRow("slots-" + harness);
        const title = harness === "codex" ? "Codex" : harness === "claude" ? "Claude" : harness;
        const desc = t(harness === "claude" ? "js.settings.account.claude_slots_description" : "js.settings.account.codex_slots_description");
        const line = append(el("div", "klabel-line"),
          el("span", "klabel", title),
          el("span", "tag", t("js.settings.account.slot_count", list.length)));
        const info = append(el("div", "kmeta"), line, el("p", "kdesc", desc));
        const rail = el("div", "account-rail");
        const signActive = pending(slotSignIn) && slotSignIn.harness === harness;
        const anySignActive = pending(slotSignIn);
        if (signActive) {
          rail.append(busyPill(slotSignIn.state === "cancelling" ? "js.settings.account.cancelling" : "js.settings.account.signing_in"));
          const cancel = btn(t("js.settings.account.cancel"), () => changeSlot("slots/sign-in/cancel", {attempt: slotSignIn.attempt}));
          cancel.disabled = slotSignIn.state === "cancelling" || !slotSignIn.attempt;
          rail.append(cancel);
        } else {
          if (modes.includes("browser")) {
            const browserBtn = btn(t("js.settings.account.slot_sign_in_browser"), () => changeSlot("slots/sign-in/start", {harness, mode: "browser"}), true);
            browserBtn.disabled = slotBusy || anySignActive || !nav.status?.loopback_peer;
            rail.append(browserBtn);
          }
          if (modes.includes("device_auth")) {
            const deviceBtn = btn(t("js.settings.account.slot_sign_in_device"), () => changeSlot("slots/sign-in/start", {harness, mode: "device_auth"}));
            deviceBtn.disabled = slotBusy || anySignActive || !nav.status?.loopback_peer;
            rail.append(deviceBtn);
          }
          const existingBtn = btn(t("js.settings.account.slot_add_existing"), () => showSlotAddEditor(harness));
          existingBtn.disabled = slotBusy || anySignActive;
          rail.append(existingBtn);
        }
        row.append(info, rail);

        if (list.length) {
          const slotList = el("div", "account-slot-list");
          for (const slot of list) {
            const item = el("div", "account-slot-item" + (slot.active ? " active" : ""));
            const label = slot.id === "default" ? t("js.settings.account.slot_default") : (slot.label || slot.id);
            const itemMeta = append(el("div", "account-slot-meta"), el("span", "account-slot-label", label));
            if (slot.id !== "default") {
              itemMeta.append(el("span", "tag", t(slot.managed ? "js.settings.account.slot_managed" : "js.settings.account.slot_custom")));
              if (slot.config_dir) {
                const pathEl = el("code", "account-slot-path", slot.config_dir);
                pathEl.title = slot.config_dir;
                itemMeta.append(pathEl);
              }
            }
            if (slot.active) itemMeta.append(pill(t("js.settings.account.active"), "ok", "", true));
            const actions = el("div", "account-slot-actions");
            if (!slot.active) {
              const useBtn = btn(t("js.settings.account.use_account"), () => changeSlot("slots/switch", {harness, slot_id: slot.id}));
              useBtn.disabled = slotBusy;
              actions.append(useBtn);
            }
            if (slot.id !== "default") {
              actions.append(hold("i-trash", t("js.settings.account.slot_remove", label), () => changeSlot("slots/remove", {harness, slot_id: slot.id, delete_dir: !!slot.managed})));
            }
            item.append(itemMeta, actions);
            slotList.append(item);
          }
          row.append(slotList);
        }

        if (signActive && slotSignIn.state !== "cancelling") {
          const drawer = el("div", "account-drawer account-signin-drawer");
          drawer.append(message(t("js.settings.account.slot_login_started")));
          if (slotSignIn.mode === "device_auth") {
            if (slotSignIn.verify_url) {
              const vInp = el("input", "inp mono");
              vInp.readOnly = true;
              vInp.value = slotSignIn.verify_url;
              const copyV = btn(t("js.settings.account.copy_link"), () => copyText(slotSignIn.verify_url, copyV));
              const openV = el("a", "btn", t("js.settings.account.open_link"));
              openV.href = slotSignIn.verify_url;
              openV.target = "_blank";
              openV.rel = "noopener noreferrer";
              drawer.append(append(el("div", "account-field"), el("label", "", t("js.settings.account.slot_device_url_label")),
                append(el("div", "account-copy-row"), vInp, copyV, append(openV, svgIcon("l-external-link")))));
            }
            if (slotSignIn.user_code) {
              const copyCode = btn(t("js.settings.account.copy_code"), () => copyText(slotSignIn.user_code, copyCode));
              const codeBox = append(el("div", "account-copy-row"),
                el("span", "account-device-code", slotSignIn.user_code), copyCode);
              drawer.append(append(el("div", "account-field"), el("label", "", t("js.settings.account.slot_device_code_label")), codeBox,
                message(t("js.settings.account.slot_device_help"))));
            }
          } else {
            if (slotSignIn.authorize_url) {
              const aInp = el("input", "inp mono");
              aInp.readOnly = true;
              aInp.value = slotSignIn.authorize_url;
              const copyA = btn(t("js.settings.account.copy_link"), () => copyText(slotSignIn.authorize_url, copyA));
              const openA = el("a", "btn", t("js.settings.account.open_link"));
              openA.href = slotSignIn.authorize_url;
              openA.target = "_blank";
              openA.rel = "noopener noreferrer";
              drawer.append(append(el("div", "account-field"), el("label", "", t("js.settings.account.signin_url_label")),
                append(el("div", "account-copy-row"), aInp, copyA, append(openA, svgIcon("l-external-link")))));
            }
            if (slotSignIn.callback_ready || slotSignIn.stdin_ready || slotSignIn.authorize_url) {
              const cbForm = el("form", "account-form");
              cbForm.autocomplete = "off";
              const cbInp = el("input", "inp mono");
              cbInp.id = "account-slot-paste-" + harness;
              cbInp.dataset.slotPaste = "1";
              cbInp.dataset.attempt = slotSignIn.attempt || "";
              cbInp.spellcheck = false;
              cbInp.autocomplete = "off";
              cbInp.value = preservedPaste;
              const submit = btn(t("js.settings.account.signin_submit"), null, true);
              submit.type = "submit";
              submit.disabled = slotBusy;
              cbForm.append(field(t("js.settings.account.slot_callback_paste_label"), cbInp, message(t("js.settings.account.slot_callback_paste_help"))),
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
          row.append(drawer);
        }

        if (slotAddForm?.harness === harness) row.append(slotAddForm.form);

        let slotNotice = "", slotTone = "";
        if (slotSignIn?.harness === harness && slotSignIn.state === "failed" && !pending(slotSignIn)) {
          slotNotice = errorText(slotSignIn.reason, "js.settings.account.slot_login_failed");
          slotTone = "fail";
        } else if (slotSignIn?.harness === harness && slotSignIn.state === "cancelled") {
          slotNotice = t("js.settings.account.sign_in_cancelled");
        }
        if (slotError) {
          slotNotice = slotError;
          slotTone = "fail";
        }
        if (slotNotice) info.append(message(slotNotice, slotTone));
        slotsGroup.append(row);
      }

      for (const item of singleAuths) {
        const harness = item.harness;
        const activeAttempt = pending(harnessAuthSignIn) && harnessAuthSignIn.harness === harness ? harnessAuthSignIn : (pending(item.sign_in) ? item.sign_in : null);
        const lastAttempt = harnessAuthSignIn?.harness === harness ? harnessAuthSignIn : item.sign_in;
        const row = accountRow("harness-auth-" + harness);
        row.classList.toggle("account-configured", !!item.probed && !item.auth_required && !item.status && !activeAttempt);
        const line = append(el("div", "klabel-line"),
          el("span", "klabel", item.label || harness),
          el("span", "tag", t("js.settings.account.single_slot_tag")));
        const info = append(el("div", "kmeta"), line, el("p", "kdesc", t("js.settings.account.agy_auth_description")));
        const rail = el("div", "account-rail");
        if (activeAttempt) {
          rail.append(busyPill(activeAttempt.state === "cancelling" ? "js.settings.account.cancelling" : "js.settings.account.signing_in"));
          const cancel = btn(t("js.settings.account.cancel"), () => changeHarnessAuth("harness-auth/cancel", {harness, attempt: activeAttempt.attempt}));
          cancel.disabled = harnessAuthBusy || activeAttempt.state === "cancelling" || !activeAttempt.attempt;
          rail.append(cancel);
        } else {
          let pillLabel = "js.settings.account.status_unknown", pillTone = "unset";
          if (item.probing) {
            rail.append(busyPill("js.settings.account.checking"));
          } else {
            if (item.auth_required) {
              pillLabel = "js.settings.account.reauth_required";
              pillTone = "fail";
            } else if (item.probed && !item.status) {
              pillLabel = "js.settings.account.signed_in";
              pillTone = "ok";
            }
            rail.append(pill(t(pillLabel), pillTone, "", pillTone === "ok"));
          }
          if (item.auth_required || !item.probed || item.status) {
            const signBtn = btn(t(item.auth_required ? "js.settings.account.sign_in_again" : "js.settings.account.sign_in"),
              () => changeHarnessAuth("harness-auth/start", {harness}), !!item.auth_required);
            signBtn.disabled = harnessAuthBusy || pending(harnessAuthSignIn) || !nav.status?.loopback_peer;
            rail.append(append(signBtn, svgIcon("l-external-link")));
          }
          const reverifyBtn = btn(t("js.settings.account.reverify"), () => changeHarnessAuth("harness-auth/reverify", {harness}));
          reverifyBtn.disabled = harnessAuthBusy || item.probing || !nav.status?.loopback_peer;
          rail.append(reverifyBtn);
        }
        row.append(info, rail);

        if (activeAttempt && activeAttempt.state !== "cancelling") {
          const drawer = el("div", "account-drawer account-signin-drawer");
          if (activeAttempt.browser_error) drawer.append(message(t("js.settings.account.signin_browser_fallback"), "warn"));
          drawer.append(message(t("js.settings.account.harness_auth_started")));
          if (activeAttempt.authorize_url) {
            const aInp = el("input", "inp mono");
            aInp.readOnly = true;
            aInp.value = activeAttempt.authorize_url;
            const copyA = btn(t("js.settings.account.copy_link"), () => copyText(activeAttempt.authorize_url, copyA));
            const openA = el("a", "btn", t("js.settings.account.open_link"));
            openA.href = activeAttempt.authorize_url;
            openA.target = "_blank";
            openA.rel = "noopener noreferrer";
            drawer.append(append(el("div", "account-field"), el("label", "", t("js.settings.account.signin_url_label")),
              append(el("div", "account-copy-row"), aInp, copyA, append(openA, svgIcon("l-external-link")))));
          }
          if (activeAttempt.stdin_ready || activeAttempt.callback_ready || activeAttempt.authorize_url) {
            const pForm = el("form", "account-form");
            pForm.autocomplete = "off";
            const pInp = el("input", "inp mono");
            pInp.id = "account-harness-auth-paste-" + harness;
            pInp.dataset.harnessAuthPaste = "1";
            pInp.dataset.attempt = activeAttempt.attempt || "";
            pInp.spellcheck = false;
            pInp.autocomplete = "off";
            pInp.value = preservedAuthPaste;
            const submit = btn(t("js.settings.account.signin_submit"), null, true);
            submit.type = "submit";
            submit.disabled = harnessAuthBusy;
            pForm.append(field(t("js.settings.account.harness_auth_paste_label"), pInp, message(t("js.settings.account.harness_auth_paste_help"))),
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
          row.append(drawer);
        }

        let authNotice = "", authTone = "";
        if (lastAttempt?.state === "failed" && !activeAttempt) {
          authNotice = errorText(lastAttempt.reason, "js.settings.account.slot_login_failed");
          authTone = "fail";
        } else if (lastAttempt?.state === "cancelled" && !activeAttempt) {
          authNotice = t("js.settings.account.sign_in_cancelled");
        }
        if (harnessAuthError) {
          authNotice = harnessAuthError;
          authTone = "fail";
        }
        if (authNotice) info.append(message(authNotice, authTone));
        slotsGroup.append(row);
      }
    }

    function updateConsent(account) {
      const visible = !!account?.client_id && !pending(signIn);
      consentRow.hidden = !visible;
      if (!visible) return;
      const allowed = account.background_consent;
      const usable = nav.status.openai_ready;
      const tag = allowed === true ? "js.settings.account.consent_allowed" :
        allowed === false ? "js.settings.account.consent_denied" : "js.settings.account.consent_unset";
      consentRow.querySelector(".tag").textContent = t(tag);
      const toggle = consentRow.querySelector(".tg");
      toggle.setAttribute("aria-checked", String(allowed === true));
      toggle.disabled = openAIBusy || (!usable && allowed !== true);
      consentRow.querySelector(".tgl").textContent = t(allowed === true ? "js.settings.on" : "js.settings.off");
    }

    function makeOpenAIRow() {
      const row = accountRow("openai");
      const info = meta("OpenAI", t("js.settings.account.openai_description"), {key: "openai", tag: t("js.settings.account.sign_in"), provider: "openai", modelLink: "js.settings.account.openai_models"});
      row.append(info, el("div", "account-rail"));
      return row;
    }

    function makeConsentRow() {
      const row = accountRow("openai-consent");
      row.classList.add("account-sub");
      const toggle = button("tg", "", () => {
        const account = nav.status.openai;
        changeOpenAI("consent", {client_id: account.client_id, allowed: account.background_consent !== true});
      });
      toggle.setAttribute("role", "switch");
      toggle.setAttribute("aria-label", t("js.settings.account.consent_title"));
      return append(row, append(el("div", "kmeta"),
        append(el("div", "klabel-line"), el("span", "klabel", t("js.settings.account.consent_title")), el("span", "tag")),
        el("p", "kdesc", t("js.settings.account.consent_description"))),
        append(el("div", "account-rail"), toggle, el("span", "tgl")));
    }

    function makeEndpointRow() {
      const row = accountRow("local");
      const info = meta(window.wsT(endpointSpec.label), window.wsT(endpointSpec.description),
        {key: endpointKey, provider: "local", modelLink: "js.settings.account.local_models"});
      info.querySelector(".klabel-line").append(el("span", "tag"));
      const rail = el("div", "account-rail");
      const detail = el("details", "account-json");
      detail.append(el("summary", "", t("js.settings.account.show_json")), el("pre"));
      row.append(info, rail, append(el("div", "account-endpoints"), el("div", "account-endpoint-list"),
        append(el("div", "account-json-footer"), detail)));
      return row;
    }

    function updateEndpointMasks() {
      endpointRow.querySelectorAll("[data-endpoint-name]").forEach(row => {
        const name = row.dataset.endpointName;
        const key = localKey(name);
        const mask = row.querySelector(".account-endpoint-key");
        mask.classList.toggle("ok", !!key?.configured);
        mask.textContent = key?.configured ? t("js.settings.account.key_set") : t("js.settings.account.no_key");
        if (key?.configured && key.masked) mask.append(" ", el("span", "account-mask", key.masked));
        const remove = row.querySelector(".account-endpoint-remove");
        remove.title = t(key?.configured ? "js.settings.account.remove_endpoint_key" : "js.settings.account.remove_endpoint", name);
        remove.setAttribute("aria-label", remove.title);
      });
    }

    function syncEndpointControls() {
      const rail = endpointRow.querySelector(".account-rail");
      const list = endpointRow.querySelector(".account-endpoint-list");
      const add = rail.querySelector("button");
      if (add) add.hidden = !!epForm;
      list.querySelectorAll("[data-endpoint-name] button").forEach(control => {
        control.disabled = endpointBusy || !!epForm;
      });
    }

    function updateEndpoints() {
      const list = endpointRow.querySelector(".account-endpoint-list");
      const rail = endpointRow.querySelector(".account-rail");
      const detail = endpointRow.querySelector(".account-json");
      const entries = Object.entries(nav.projection || {}).sort(([left], [right]) => left.localeCompare(right));
      endpointRow.querySelector(".tag").textContent = nav.projection ? t("js.settings.account.endpoint_count", entries.length) : "";
      list.replaceChildren();
      rail.replaceChildren();
      detail.hidden = !nav.projection;
      if (endpointError) {
        const notice = message(endpointError, "fail");
        notice.classList.add("account-endpoint-status");
        notice.append(" ", btn(t("js.settings.account.retry"), loadEndpoints));
        list.append(notice);
      }
      if (!nav.projection) {
        if (!endpointError) list.append(el("p", "account-endpoint-status", t("js.settings.loading")));
        return;
      }
      detail.querySelector("pre").textContent = JSON.stringify(nav.projection, null, 2);
      rail.append(btn(t("js.settings.account.add_endpoint"), () => showEndpointEditor()));
      if (entries.length) {
        list.append(append(el("div", "account-ep account-ep-head"), ...[t("js.settings.account.name"), t("js.settings.account.base_url"), t("js.settings.account.kind"), t("js.settings.account.api_key")].map(label => el("span", "", label))));
      } else if (!epForm) {
        list.append(el("p", "account-endpoint-status", t("js.settings.account.endpoints_empty")));
      }
      for (const [name, value] of entries) {
        if (!value || typeof value !== "object") continue;
        const row = el("div", "account-ep");
        row.dataset.endpointName = name;
        row.tabIndex = -1;
        const url = el("code", "account-endpoint-url", value.base_url);
        url.title = value.base_url || "";
        const edit = iconButton("i-pencil", t("js.settings.account.edit_endpoint", name), () => showEndpointEditor(name, value, row));
        const remove = hold("i-trash", t("js.settings.account.remove_endpoint", name), () => changeEndpoint({op: "remove", name}));
        remove.classList.add("account-endpoint-remove");
        row.append(append(el("div", "account-endpoint-name"), el("b", "", name), el("code", "", "local/" + name)),
          url, el("code", "account-endpoint-kind", value.kind), el("span", "account-endpoint-key"),
          append(el("div", "account-endpoint-actions"), edit, remove));
        list.append(row);
        if (epForm?.name === name) {
          epForm.row = row;
          row.classList.add("account-editing");
          list.append(epForm.form);
        }
      }
      if (epForm && !epForm.name) list.append(epForm.form);
      updateEndpointMasks();
      syncEndpointControls();
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
      const form = el("form", "account-ep-edit");
      form.autocomplete = "off";
      const id = el("input", "inp mono");
      id.id = "account-endpoint-name";
      id.value = name || "";
      id.placeholder = "ollama-box";
      id.readOnly = !!name;
      id.spellcheck = false;
      id.required = true;
      id.maxLength = 64;
      const url = el("input", "inp mono");
      url.id = "account-endpoint-url";
      url.value = value?.base_url || "";
      url.placeholder = "http://localhost:11434/v1";
      url.spellcheck = false;
      url.required = true;
      const input = password("account-endpoint-key", t("js.settings.account.optional_key_hint"));
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
      const nameHint = message(t("js.settings.account.name_help"));
      const urlHint = message(t("js.settings.account.url_help"));
      const keyHint = message(t("js.settings.account.endpoint_key_help"));
      const save = btn(name ? t("js.settings.save") : t("js.settings.account.add"), null, true);
      save.type = "submit";
      const cancel = btn(t("js.settings.account.cancel"), () => {
        closeEndpointEditor();
        (row?.querySelector("button") || endpointRow.querySelector(".account-rail button"))?.focus();
      });
      const error = message("", "fail");
      error.hidden = true;
      const warning = message("", "warn");
      warning.classList.add("inline-warn");
      warning.hidden = true;
      form.append(append(el("div", "account-ep-grid"),
        field(t("js.settings.account.name"), id, nameHint), field(t("js.settings.account.base_url"), url, urlHint),
        field(t("js.settings.account.kind"), modes), field(t("js.settings.account.optional_key"), input, keyHint)), warning, error,
        append(el("div", "account-ep-foot"), save, cancel));
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
        endpointRow.querySelector(".account-endpoint-list").append(form);
      }
      syncEndpointControls();
      validate();
      (name ? url : id).focus();
    }

    async function changeEndpoint(body, editor) {
      if (endpointBusy) return;
      endpointBusy = true;
      endpointError = "";
      syncEndpointControls();
      if (editor) {
        editor.form.querySelectorAll("input, button").forEach(control => { control.disabled = true; });
        editor.error.hidden = true;
      }
      try {
        const request = accountRequest("endpoints", body);
        delete body.key;
        const result = await request;
        if (result.changed) window.WsSettingsCombobox?.invalidateChoices();
        if (editor && epForm === editor) closeEndpointEditor();
        nav.projection = Object.fromEntries(result.endpoints.map(({name, base_url, kind}) => [name, {base_url, kind}]));
        await Promise.all([loadEndpoints(), refreshStatus()]);
        if (endpointRow.isConnected) {
          const changed = Array.from(endpointRow.querySelectorAll("[data-endpoint-name]")).find(row => row.dataset.endpointName === body.name);
          (changed || endpointRow).focus({preventScroll: true});
        }
      } catch (error) {
        const notice = errorText(error.code);
        if (error.code === "endpoint_key_retired") await refreshStatus();
        if (editor && epForm === editor) {
          editor.error.hidden = false;
          editor.error.textContent = notice;
        } else {
          endpointError = notice;
          updateEndpoints();
        }
      } finally {
        endpointBusy = false;
        if (editor && epForm === editor) editor.form.querySelectorAll("input, button").forEach(control => { control.disabled = false; });
        syncEndpointControls();
      }
    }

    openRouterRow = accountRow("openrouter");
    openRouterRow.append(meta("OpenRouter", t("js.settings.account.openrouter_description"),
      {key: "openrouter", tag: t("js.settings.account.api_key"), provider: "openrouter", modelLink: "js.settings.account.openrouter_models"}), el("div", "account-rail"));
    openAIRow = makeOpenAIRow();
    consentRow = makeConsentRow();
    endpointRow = makeEndpointRow();
    providers.append(openRouterRow, openAIRow, consentRow);
    if (window.WsSettingsVoice && typeof window.WsSettingsVoice.create === "function") {
      voiceSection = window.WsSettingsVoice.create({
        el, button, append, svgIcon, request, accountRequest, hold, pill, busyPill, message, password, field, errorText,
        getStatus: () => nav.status,
        getEndpoints: () => nav.projection,
        onChange,
        onOpenLocal: () => {
          endpointRow.focus({preventScroll: true});
          endpointRow.scrollIntoView({block: "nearest"});
        }
      });
      voiceHost.append(voiceSection.element);
    }
    endpoints.append(endpointRow);
    updateOpenAI();
    updateEndpoints();

    return {
      panel, attention, providerIssue, refreshStatus,
      ensureEndpoints(force) { if (force || !nav.isLoaded) loadEndpoints(); },
      closeEditors() {
        closeOpenRouterEditor();
        updateOpenRouter();
        if (voiceSection) voiceSection.closeEditors();
        closeEndpointEditor();
        closeSlotAddEditor();
        updateSlots();
      },
      focus(provider) {
        if (voiceSection && voiceSection.focus(provider)) return;
        const isEndpoint = provider.startsWith("local/");
        const name = isEndpoint ? provider.slice(6) : provider;
        const node = isEndpoint ? Array.from(endpointRow.querySelectorAll("[data-endpoint-name]")).find(row => row.dataset.endpointName === name) || endpointRow :
          (provider === "openai" ? openAIRow : provider === "openrouter" ? openRouterRow : endpointRow);
        node.focus({preventScroll: true});
        node.scrollIntoView({block: "nearest"});
      }
    };
  }

  window.WsSettingsAccount = {create};
}());
