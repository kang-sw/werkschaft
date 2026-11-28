/* Machine-wide provider settings coordinator. Only password inputs hold unsaved keys. */
(function () {
  "use strict";

  function create({
    el, button, append, svgIcon, request, showTiers, onChange, endpointSpec,
    openVoice, isVoiceOpenRouterShared, onStatusChange, onConfigSnapshot
  }) {
    const shared = window.WsSettingsAccountShared;
    const t = window.wsT;
    const h = shared.createHelpers({el, button, append, svgIcon, t});
    const nav = {status: null, projection: null, isLoaded: false, error: false, endpointError: ""};
    let statusLoad = null, endpointLoad = null;

    const panel = el("div", "account-panel");
    const providerNotice = el("p", "info-note");
    providerNotice.hidden = true;
    providerNotice.setAttribute("role", "status");

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

    let harnessGroup, runtimeGroup, pirpcGroup;

    function refreshStatus() {
      if (statusLoad) return statusLoad;
      statusLoad = shared.accountRequest("status").then(result => {
        const before = nav.status;
        nav.status = result;
        nav.error = false;
        harnessGroup.applyStatus(before, result);
        runtimeGroup.applyStatus(before, result);
        if (pirpcGroup) pirpcGroup.applyStatus(before, result);
        if (typeof onStatusChange === "function" &&
            (JSON.stringify(before?.voice_keys) !== JSON.stringify(result.voice_keys) ||
             JSON.stringify(before?.openrouter) !== JSON.stringify(result.openrouter))) {
          onStatusChange(result);
        }
      }).catch(() => {
        nav.error = true;
      }).finally(() => {
        statusLoad = null;
        providerNotice.replaceChildren();
        providerNotice.hidden = !nav.error;
        if (nav.error) {
          providerNotice.append(t("js.settings.account.status_failed"), " ", h.btn(t("js.settings.account.retry"), refreshStatus));
        }
        onChange();
      });
      return statusLoad;
    }

    function loadEndpoints() {
      if (endpointLoad) return endpointLoad;
      endpointLoad = request("/api/config/snapshot?scope=global").then(config => {
        if (typeof onConfigSnapshot === "function") onConfigSnapshot(config);
        const entry = config.entries.find(item => item.key === shared.endpointKey);
        if (!entry) throw new Error();
        const value = typeof entry.value === "string" ? JSON.parse(entry.value.trim() || "{}") : entry.value;
        nav.projection = value && typeof value === "object" && !Array.isArray(value) ? value : {};
        nav.endpointError = "";
      }).catch(() => {
        nav.endpointError = t("js.settings.account.endpoints_failed");
      }).finally(() => {
        nav.isLoaded = true;
        endpointLoad = null;
        runtimeGroup.updateEndpoints();
        runtimeGroup.updateOpenRouter();
        onChange();
      });
      return endpointLoad;
    }

    const ctx = {
      el, button, append, svgIcon, t, h, nav, endpointSpec,
      endpointKey: shared.endpointKey,
      accountRequest: shared.accountRequest,
      pending: shared.pending,
      showTiers, openVoice, isVoiceOpenRouterShared, onStatusChange,
      refreshStatus, loadEndpoints, onChange
    };

    harnessGroup = window.WsSettingsAccountHarness.create(ctx);
    runtimeGroup = window.WsSettingsAccountRuntime.create(ctx);
    pirpcGroup = window.WsSettingsAccountPiRPC ? window.WsSettingsAccountPiRPC.create(ctx) : null;

    panel.append(
      el("p", "info-note", t("js.settings.account.machine_note")),
      providerNotice,
      harnessGroup.element,
      runtimeGroup.element
    );
    if (pirpcGroup) panel.append(pirpcGroup.element);

    return {
      panel,
      attention,
      providerIssue,
      refreshStatus,
      ensureEndpoints(force) { if (force || !nav.isLoaded) loadEndpoints(); },
      syncVoiceState() { runtimeGroup.updateOpenRouter(); },
      voiceDeps() {
        return {
          el, button, append, svgIcon, request,
          accountRequest: shared.accountRequest,
          hold: h.holdIcon,
          pill: h.pill,
          busyPill: h.busyPill,
          message: h.message,
          password: h.password,
          field: h.field,
          errorText: h.errorText,
          getStatus: () => nav.status,
          getEndpoints: () => nav.projection
        };
      },
      closeEditors() {
        harnessGroup.closeEditors();
        runtimeGroup.closeEditors();
        if (pirpcGroup) pirpcGroup.closeEditors();
      },
      focus(provider) {
        if (!provider) return;
        const harnessNode = harnessGroup.element.querySelector('[data-account-row="slots-' + provider + '"], [data-account-row="harness-auth-' + provider + '"]');
        if (harnessNode) {
          harnessNode.focus({preventScroll: true});
          harnessNode.scrollIntoView({block: "nearest"});
          return;
        }
        if (pirpcGroup && pirpcGroup.focus(provider)) return;
        runtimeGroup.focus(provider);
      },
      destroy() {
        harnessGroup.destroy();
        runtimeGroup.destroy();
        if (pirpcGroup) pirpcGroup.destroy();
      }
    };
  }

  window.WsSettingsAccount = {create};
}());
