/* Shared request, error, hold-confirm, and 3-level hierarchy DOM helpers for Settings -> Account. */
(function () {
  "use strict";

  const endpointKey = "agent.werk_endpoints";
  const pending = attempt => !!(attempt && ["starting", "waiting", "cancelling"].includes(attempt.state));

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

  function createHelpers({el, button, append, svgIcon, t}) {
    const btn = (label, action, primary) => button("btn" + (primary ? " pri" : ""), label, action);
    const link = (label, action) => append(button("account-link", label, action), svgIcon("l-chevron-right"));

    function ab(label, action, opts) {
      const o = opts || {};
      const cls = "acc-ab" + (o.cls ? " " + o.cls : "");
      const node = button(cls, "", action);
      if (o.submit) node.type = "submit";
      if (o.ariaLabel) node.setAttribute("aria-label", o.ariaLabel);
      if (o.title) node.title = o.title;
      if (o.disabled) node.disabled = true;
      node.append(el("span", "", label));
      if (o.icon) node.append(svgIcon(o.icon));
      return node;
    }

    function holdBar(label, ariaLabel, action, opts) {
      const o = opts || {};
      const node = ab(label, null, {
        cls: "danger" + (o.cls ? " " + o.cls : ""),
        ariaLabel: ariaLabel || label,
        title: (ariaLabel || label) + " · " + t("js.settings.account.hold_hint"),
        disabled: o.disabled
      });
      if (window.WsHoldConfirm) {
        window.WsHoldConfirm.attach(node, {mode: "bar", onConfirm: action});
      }
      return node;
    }

    function iconButton(iconName, label, action) {
      const node = append(button("reset-icon-btn account-icon", "", action), svgIcon(iconName));
      node.title = label;
      node.setAttribute("aria-label", label);
      return node;
    }

    function holdIcon(iconName, label, action) {
      const node = iconButton(iconName, label);
      if (window.WsHoldConfirm) {
        window.WsHoldConfirm.attach(node, {mode: "radial", onConfirm: action});
      }
      return node;
    }

    function pill(label, tone, detail, icon) {
      const node = el("span", "acc-pill" + (tone ? " " + tone : "") + (icon ? " has-check" : " dot"));
      if (icon) {
        const check = el("span", "acc-check", "✓");
        check.setAttribute("aria-hidden", "true");
        node.append(check);
      }
      node.append(el("span", "lbl", label));
      if (detail) node.append(el("span", "account-mask", detail));
      return node;
    }

    function busyPill(labelKey, detail) {
      return pill(t(labelKey), "busy", detail);
    }

    function message(text, tone = "") {
      const node = el("p", "hint account-message" + (tone ? " " + tone : ""), text);
      if (tone === "fail") node.setAttribute("role", "alert");
      return node;
    }

    function password(id, placeholder) {
      const input = el("input", "inp mono acc-inp");
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
        invalid_slot: "js.settings.account.slot_label_too_long",
        default_slot_immutable: "js.settings.account.slot_default_immutable",
        account_slot_not_found: "js.settings.account.slot_not_found",
        config_dir_is_default: "js.settings.account.slot_dir_default",
        config_dir_not_found: "js.settings.account.slot_dir_missing",
        invalid_config_dir: "js.settings.account.slot_dir_missing",
        cli_unavailable: "js.settings.account.slot_cli_unavailable",
        auth_preflight_failed: "js.settings.account.slot_preflight_failed",
        auth_not_ready: "js.settings.account.slot_preflight_failed",
        login_failed: "js.settings.account.slot_login_failed",
        timed_out: "js.settings.account.timed_out",
        runtime_unavailable: "js.settings.account.pi_runtime_unavailable",
        runtime_not_installed: "js.settings.account.pi_runtime_not_installed",
        runtime_busy: "js.settings.account.pi_runtime_busy",
        pty_unavailable: "js.settings.account.pi_pty_unavailable",
        login_in_progress: "js.settings.account.pi_login_in_progress",
        invalid_provider: "js.settings.account.pi_invalid_provider"
      };
      return t(Object.hasOwn(errors, code) ? errors[code] : fallback);
    }

    function copyText(text, control) {
      if (!text) return;
      const done = () => {
        if (!control) return;
        const target = control.querySelector("span") || control;
        const prev = target.textContent;
        target.textContent = t("js.settings.account.copied");
        window.setTimeout(() => { if (control.isConnected) target.textContent = prev; }, 1400);
      };
      if (navigator.clipboard && typeof navigator.clipboard.writeText === "function") {
        navigator.clipboard.writeText(text).then(done).catch(() => {});
      }
    }

    function groupCard(num, title, subtitle) {
      const root = el("section", "acc-grp");
      const head = el("div", "acc-grp-h");
      const numEl = el("div", "acc-grp-n", num);
      numEl.setAttribute("aria-hidden", "true");
      const textWrap = append(el("div", "acc-grp-t"), el("h3", "", title), el("p", "", subtitle));
      head.append(numEl, textWrap);
      const body = el("div", "acc-grp-body");
      root.append(head, body);
      return {root, body};
    }

    function providerBlock({rowName, iconClass, monogram, title, tag, subtitle, modelLink, onModelLink}) {
      const root = el("div", "acc-prov");
      root.dataset.accountRow = rowName;
      root.tabIndex = -1;
      const head = el("div", "acc-prov-h");
      const icon = el("div", "acc-mono-ic " + iconClass, monogram);
      icon.setAttribute("aria-hidden", "true");
      const titleEl = el("b", "", title);
      const tagEl = tag ? el("span", "acc-tag", tag) : null;
      if (tagEl) titleEl.append(" ", tagEl);
      const subEl = el("span", "acc-prov-sub", subtitle || "");
      const idWrap = append(el("div", "acc-prov-id"), titleEl, subEl);
      if (modelLink && onModelLink) idWrap.append(link(t(modelLink), onModelLink));
      const noticeSlot = el("div", "acc-prov-notice");
      noticeSlot.hidden = true;
      idWrap.append(noticeSlot);
      const acts = el("div", "acc-prov-act");
      head.append(icon, idWrap, acts);
      const kids = el("div", "acc-kids");
      root.append(head, kids);
      return {root, head, titleEl, tagEl, subEl, idWrap, noticeSlot, acts, kids};
    }

    function setNotice(slot, text, tone) {
      slot.replaceChildren();
      slot.hidden = !text;
      if (text) slot.append(message(text, tone || ""));
    }

    function captureFocus(container) {
      const active = document.activeElement;
      if (!active || !container || !container.contains(active)) return null;
      return {
        node: active,
        id: active.id || "",
        selectionStart: typeof active.selectionStart === "number" ? active.selectionStart : null,
        selectionEnd: typeof active.selectionEnd === "number" ? active.selectionEnd : null
      };
    }

    function restoreFocus(container, saved) {
      if (!saved || !container) return;
      let target = saved.node && saved.node.isConnected && container.contains(saved.node) ? saved.node : null;
      if (!target && saved.id) target = container.querySelector("#" + saved.id);
      if (!target || typeof target.focus !== "function") return;
      target.focus({preventScroll: true});
      if (saved.selectionStart !== null && typeof target.setSelectionRange === "function") {
        try { target.setSelectionRange(saved.selectionStart, saved.selectionEnd ?? saved.selectionStart); } catch (_) {}
      }
    }

    return {
      btn, link, ab, holdBar, holdIcon, iconButton, pill, busyPill,
      message, password, field, errorText, copyText, groupCard,
      providerBlock, setNotice, captureFocus, restoreFocus
    };
  }

  window.WsSettingsAccountShared = {endpointKey, pending, accountRequest, createHelpers};
}());
