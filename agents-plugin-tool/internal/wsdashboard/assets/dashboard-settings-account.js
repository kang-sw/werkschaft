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
    const endpoints = el("div", "sgroup");
    const providerNotice = el("p", "info-note");
    const nav = {status: null, projection: null, isLoaded: false, error: false};
    let statusLoad, endpointLoad, signIn, pollTimer;
    let openRouterBusy = false, openAIBusy = false, endpointBusy = false;
    let orForm, epForm, openRouterRow, openAIRow, consentRow, endpointRow;
    let openRouterError = "", openAIError = "", endpointError = "";

    providerNotice.hidden = true;
    providerNotice.setAttribute("role", "status");
    panel.append(el("p", "info-note", t("js.settings.account.machine_note")), providerNotice,
      section("js.settings.account.providers"), providers, section("js.settings.account.endpoints"), endpoints);

    function errorText(code, fallback = "js.settings.account.request_failed") {
      const errors = {
        invalid_key: "js.settings.account.invalid_key",
        key_rejected: "js.settings.account.key_rejected",
        invalid_endpoint: "js.settings.account.invalid_endpoint",
        endpoint_key_retired: "js.settings.account.endpoint_key_retired",
        credential_changed: "js.settings.account.credential_changed"
      };
      return t(Object.hasOwn(errors, code) ? errors[code] : fallback);
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
        nav.status = result;
        nav.error = false;
        if (!openAIBusy && JSON.stringify(signIn) !== JSON.stringify(result.sign_in)) signIn = result.sign_in;
        if (pending(signIn)) schedulePoll();
        if (JSON.stringify(before?.openrouter) !== JSON.stringify(result.openrouter)) updateOpenRouter();
        if (before?.openai_ready !== result.openai_ready || before?.loopback_peer !== result.loopback_peer ||
            previousAttempt !== signIn || JSON.stringify(before?.openai) !== JSON.stringify(result.openai)) updateOpenAI();
        if (JSON.stringify(before?.local_keys) !== JSON.stringify(result.local_keys)) updateEndpointMasks();
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
        if (result.state === before.state && result.attempt === before.attempt && !openAIError) return;
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
        if (path === "sign-out") window.WsSettingsCombobox?.invalidateChoices();
        if (result) {
          signIn = result;
          schedulePoll();
        }
      } catch (error) {
        openAIError = errorText(error.code);
      } finally {
        openAIBusy = false;
        if (path === "sign-out" || path === "consent") await refreshStatus();
        updateOpenAI();
      }
    }

    function updateOpenAI() {
      const account = nav.status?.openai;
      const rail = openAIRow.querySelector(".account-rail");
      const info = openAIRow.querySelector(".kmeta");
      openAIRow.classList.toggle("account-configured", !!nav.status?.openai_ready && !account?.plan_paused && !pending(signIn));
      info.querySelector(".account-message")?.remove();
      rail.replaceChildren();
      let notice, tone;
      if (pending(signIn)) {
        rail.append(busyPill(signIn.state === "cancelling" ? "js.settings.account.cancelling" : "js.settings.account.signing_in"));
        const cancel = btn(t("js.settings.account.cancel"), () => changeOpenAI("sign-in/cancel", {attempt: signIn.attempt}));
        cancel.disabled = signIn.state === "cancelling" || !signIn.attempt;
        rail.append(cancel);
        notice = t("js.settings.account.browser_continue");
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
        }
        if (account?.client_id) rail.append(hold("i-power", t("js.settings.account.sign_out"), () => changeOpenAI("sign-out", {client_id: account.client_id})));
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
    endpoints.append(endpointRow);
    updateOpenAI();
    updateEndpoints();

    return {
      panel, attention, providerIssue, refreshStatus,
      ensureEndpoints(force) { if (force || !nav.isLoaded) loadEndpoints(); },
      closeEditors() {
        closeOpenRouterEditor();
        updateOpenRouter();
        closeEndpointEditor();
      },
      focus(provider) {
        const isEndpoint = provider.startsWith("local/");
        const name = isEndpoint ? provider.slice(6) : provider;
        const node = isEndpoint ? Array.from(endpointRow.querySelectorAll("[data-endpoint-name]")).find(row => row.dataset.endpointName === name) || endpointRow :
          provider === "openai" ? openAIRow : provider === "openrouter" ? openRouterRow : endpointRow;
        node.focus({preventScroll: true});
        node.scrollIntoView({block: "nearest"});
      }
    };
  }

  window.WsSettingsAccount = {create};
}());
