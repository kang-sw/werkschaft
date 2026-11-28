/* Group 3 of Settings -> Account: Managed Pi (pi-rpc) Runtime & Ephemeral PTY Login. */
(function () {
  "use strict";

  const HARNESS = "pi-rpc";
  const SYMBOLS_FONT = "WsNerdSymbols";
  const FONT_WAIT_MS = 1500;
  const FIT_DEBOUNCE_MS = 120;
  const INSTALL_POLL_MS = 700;

  const DARK_PALETTE = {
    black: "#2b2d32", red: "#f0797e", green: "#6cc39a", yellow: "#e3b341",
    blue: "#7aa2ff", magenta: "#c79bf2", cyan: "#5fc4cc", white: "#d4d5d8",
    brightBlack: "#6b7079", brightRed: "#ff9a9e", brightGreen: "#8fd9b3", brightYellow: "#f2cc6b",
    brightBlue: "#a3c0ff", brightMagenta: "#dcbcfa", brightCyan: "#8adbe1", brightWhite: "#f3f3f0"
  };
  const LIGHT_PALETTE = {
    black: "#1b1d21", red: "#c0262d", green: "#1d7348", yellow: "#8a5a00",
    blue: "#2457c5", magenta: "#8a3fb5", cyan: "#0e7a83", white: "#8b9097",
    brightBlack: "#5f646b", brightRed: "#d9434a", brightGreen: "#2a8f5c", brightYellow: "#a36b00",
    brightBlue: "#2b62d9", brightMagenta: "#a052cf", brightCyan: "#138f99", brightWhite: "#b9bdc3"
  };

  let xtermLoadPromise = null;

  function termScriptName() {
    return document.documentElement?.getAttribute("data-term") || "";
  }

  function decodeBase64Bytes(b64) {
    const raw = typeof atob === "function" ? atob(b64 || "") : "";
    const out = new Uint8Array(raw.length);
    for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
    return out;
  }

  function parseHexOrRgb(color, fallback) {
    const s = String(color || "").trim();
    let m = /^#([0-9a-f]{3}|[0-9a-f]{4}|[0-9a-f]{6}|[0-9a-f]{8})$/i.exec(s);
    if (m) {
      const h = m[1];
      if (h.length <= 4) {
        return [parseInt(h[0] + h[0], 16), parseInt(h[1] + h[1], 16), parseInt(h[2] + h[2], 16)];
      }
      return [parseInt(h.slice(0, 2), 16), parseInt(h.slice(2, 4), 16), parseInt(h.slice(4, 6), 16)];
    }
    m = /^rgba?\(\s*(\d+(?:\.\d+)?)\s*[,\s]\s*(\d+(?:\.\d+)?)\s*[,\s]\s*(\d+(?:\.\d+)?)/i.exec(s);
    if (m) return [+m[1], +m[2], +m[3]];
    return fallback;
  }

  function toHex(rgb) {
    return "#" + rgb.map(v => ("0" + Math.max(0, Math.min(255, Math.round(v))).toString(16)).slice(-2)).join("");
  }

  function buildTermTheme() {
    let bgStr = "#1e1f22";
    let fgStr = "#e6e6e6";
    if (typeof window.getComputedStyle === "function" && document.documentElement) {
      try {
        const cs = window.getComputedStyle(document.documentElement);
        bgStr = cs.getPropertyValue("--wb-card").trim() || bgStr;
        fgStr = cs.getPropertyValue("--wb-fg").trim() || fgStr;
      } catch (_) {}
    }
    const bg = parseHexOrRgb(bgStr, [30, 31, 34]);
    const fg = parseHexOrRgb(fgStr, [230, 230, 230]);
    const dark = 0.299 * bg[0] + 0.587 * bg[1] + 0.114 * bg[2] < 128;
    const pal = Object.assign({}, dark ? DARK_PALETTE : LIGHT_PALETTE);
    pal.background = toHex(bg);
    pal.foreground = toHex(fg);
    pal.cursor = toHex(fg);
    pal.cursorAccent = toHex(bg);
    pal.selectionBackground = dark ? "#3a4a6b" : "#c9d8f7";
    return pal;
  }

  function loadXterm() {
    if (window.Terminal && window.FitAddon) return Promise.resolve();
    if (!xtermLoadPromise) {
      const script = termScriptName();
      const code = new Promise((resolve, reject) => {
        if (window.Terminal && window.FitAddon) { resolve(); return; }
        if (!script) { reject(new Error("xterm_unavailable")); return; }
        const s = document.createElement("script");
        s.src = "/s/" + encodeURIComponent(script);
        s.onload = () => {
          if (window.Terminal && window.FitAddon) resolve();
          else { xtermLoadPromise = null; reject(new Error("xterm_load_failed")); }
        };
        s.onerror = () => { xtermLoadPromise = null; reject(new Error("xterm_load_failed")); };
        (document.head || document.documentElement || document.body).append(s);
      });
      const font = new Promise(resolve => {
        if (!document.fonts || typeof document.fonts.load !== "function") { resolve(); return; }
        const timer = window.setTimeout(resolve, FONT_WAIT_MS);
        document.fonts.load('13px "' + SYMBOLS_FONT + '"', "\ue0b0").then(
          () => { window.clearTimeout(timer); resolve(); },
          () => { window.clearTimeout(timer); resolve(); }
        );
      });
      xtermLoadPromise = Promise.all([code, font]);
    }
    return xtermLoadPromise;
  }

  function buildWsUrl(wsPath) {
    if (/^wss?:\/\//i.test(wsPath)) return wsPath;
    const proto = (typeof location !== "undefined" && location.protocol === "https:") ? "wss:" : "ws:";
    const host = (typeof location !== "undefined" && location.host) ? location.host : "127.0.0.1";
    return proto + "//" + host + wsPath;
  }

  function formatMegabytes(bytes) {
    return (Math.max(0, Number(bytes) || 0) / (1024 * 1024)).toFixed(1);
  }

  function invalidateChoices() {
    if (window.WsSettingsCombobox && typeof window.WsSettingsCombobox.invalidateChoices === "function") {
      window.WsSettingsCombobox.invalidateChoices();
    }
  }

  function create(ctx) {
    const {el, append, t, h, nav, accountRequest, refreshStatus} = ctx;
    const group = h.groupCard("3", t("js.settings.account.pi_group_title"), t("js.settings.account.pi_group_subtitle"));
    group.root.dataset.accountGroup = "pirpc";
    group.root.hidden = true;

    let runtimeBusy = false;
    let runtimeError = "";
    let slotBusy = false;
    let slotError = "";
    let installPollTimer = null;
    let addDrawerOpen = false;
    let addDraft = {label: "", provider: ""};
    let renameState = null; // {slotId, form, input, saveBtn, cancelBtn, errEl, busy}
    let modal = null; // active PTY login modal controller
    let destroyed = false;

    function runtimeState() {
      return nav.status?.pi_runtime || null;
    }

    function loginSnapshot() {
      return nav.status?.pi_login || null;
    }

    function isLoginRunning() {
      return !!(modal && modal.running) || loginSnapshot()?.state === "running";
    }

    function clearInstallPoll() {
      if (installPollTimer !== null) {
        window.clearTimeout(installPollTimer);
        installPollTimer = null;
      }
    }

    function scheduleInstallPoll() {
      if (destroyed || installPollTimer !== null) return;
      if (!runtimeState()?.installing) return;
      installPollTimer = window.setTimeout(async () => {
        installPollTimer = null;
        if (destroyed || !runtimeState()?.installing) return;
        try { await refreshStatus(); } catch (_) {}
        if (!destroyed && runtimeState()?.installing) scheduleInstallPoll();
      }, INSTALL_POLL_MS);
    }

    async function installRuntime() {
      if (runtimeBusy || runtimeState()?.installing || isLoginRunning()) return;
      runtimeBusy = true;
      runtimeError = "";
      render();
      try {
        const res = await accountRequest("pi/runtime/install", {});
        if (nav.status && res) nav.status.pi_runtime = res;
        if (res && !res.installing && res.installed) invalidateChoices();
        if (res?.installing) scheduleInstallPoll();
        else await refreshStatus();
      } catch (error) {
        runtimeError = h.errorText(error.code);
      } finally {
        runtimeBusy = false;
        render();
      }
    }

    async function uninstallRuntime() {
      if (runtimeBusy || runtimeState()?.installing || isLoginRunning()) return;
      runtimeBusy = true;
      runtimeError = "";
      render();
      try {
        const res = await accountRequest("pi/runtime/uninstall", {});
        if (nav.status && res) nav.status.pi_runtime = res;
        addDrawerOpen = false;
        invalidateChoices();
        await refreshStatus();
      } catch (error) {
        runtimeError = h.errorText(error.code);
      } finally {
        runtimeBusy = false;
        render();
      }
    }

    async function mutateSlot(path, body) {
      if (slotBusy) return;
      slotBusy = true;
      slotError = "";
      render();
      try {
        await accountRequest(path, body);
        if (path === "slots/switch" || path === "slots/remove") invalidateChoices();
        await refreshStatus();
      } catch (error) {
        slotError = h.errorText(error.code);
      } finally {
        slotBusy = false;
        render();
      }
    }

    function cancelRename(restoreFocus) {
      if (!renameState) return;
      const slotId = renameState.slotId;
      renameState.form.remove();
      renameState = null;
      render();
      if (restoreFocus) {
        const btn = group.body.querySelector('[data-account-row="slots-pi-rpc"] [data-slot-rename="' + slotId + '"]');
        if (btn && typeof btn.focus === "function") btn.focus({preventScroll: true});
      }
    }

    function openRename(slot) {
      if (!slot || slot.id === "default" || slotBusy) return;
      if (renameState) {
        renameState.form.remove();
        renameState = null;
      }
      const form = el("form", "acc-rename-form");
      form.autocomplete = "off";
      const input = el("input", "inp acc-inp acc-rename-inp");
      input.type = "text";
      input.id = "account-slot-rename-pi-rpc-" + slot.id;
      input.maxLength = 80;
      input.value = slot.label || slot.id;
      input.setAttribute("aria-label", t("js.settings.account.slot_rename"));
      const saveBtn = h.ab(t("js.settings.save"), null, {cls: "pri", submit: true});
      const cancelBtn = h.ab(t("js.settings.account.cancel"), () => cancelRename(true));
      const errEl = el("span", "hint account-message fail acc-rename-err");
      errEl.setAttribute("role", "alert");
      errEl.hidden = true;
      form.append(input, saveBtn, cancelBtn, errEl);

      const state = {slotId: slot.id, form, input, saveBtn, cancelBtn, errEl, busy: false};
      input.addEventListener("input", () => {
        if (!errEl.hidden) {
          errEl.hidden = true;
          input.removeAttribute("aria-invalid");
        }
      });
      input.addEventListener("keydown", ev => {
        if (ev.key === "Escape") {
          ev.preventDefault();
          ev.stopPropagation();
          cancelRename(true);
        }
      });
      form.addEventListener("submit", async ev => {
        ev.preventDefault();
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
          const updated = await accountRequest("slots/rename", {harness: HARNESS, slot_id: slot.id, label: trimmed});
          const list = nav.status?.slots?.[HARNESS];
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
          const btn = group.body.querySelector('[data-account-row="slots-pi-rpc"] [data-slot-rename="' + slot.id + '"]');
          if (btn && typeof btn.focus === "function") btn.focus({preventScroll: true});
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

    function teardownModal(restoreFocus) {
      if (!modal) return;
      const cur = modal;
      modal = null;
      cur.closed = true;
      cur.running = false;
      if (cur.fitTimer !== null) {
        window.clearTimeout(cur.fitTimer);
        cur.fitTimer = null;
      }
      if (cur.statusPollTimer !== null) {
        window.clearTimeout(cur.statusPollTimer);
        cur.statusPollTimer = null;
      }
      if (cur.onKeyDown) document.removeEventListener("keydown", cur.onKeyDown, true);
      if (cur.themeObserver) {
        try { cur.themeObserver.disconnect(); } catch (_) {}
      }
      if (cur.mediaQuery && cur.onMediaChange && typeof cur.mediaQuery.removeEventListener === "function") {
        cur.mediaQuery.removeEventListener("change", cur.onMediaChange);
      }
      if (cur.resizeObserver) {
        try { cur.resizeObserver.disconnect(); } catch (_) {}
      }
      if (cur.ws) {
        try { cur.ws.onopen = cur.ws.onmessage = cur.ws.onerror = cur.ws.onclose = null; cur.ws.close(); } catch (_) {}
      }
      if (cur.term && typeof cur.term.dispose === "function") {
        try { cur.term.dispose(); } catch (_) {}
      }
      if (cur.backdrop && cur.backdrop.parentNode) {
        cur.backdrop.remove();
      }
      if (restoreFocus && cur.opener && cur.opener.isConnected && typeof cur.opener.focus === "function") {
        cur.opener.focus({preventScroll: true});
      }
      render();
    }

    function updateModalPill(cur, state) {
      if (!cur || !cur.pillSlot) return;
      cur.pillSlot.replaceChildren();
      if (state === "succeeded") {
        cur.pillSlot.append(h.pill(t("js.settings.account.pi_modal_succeeded"), "ok", "", true));
      } else if (state === "failed") {
        cur.pillSlot.append(h.pill(t("js.settings.account.pi_modal_failed"), "fail"));
      } else if (state === "cancelled") {
        cur.pillSlot.append(h.pill(t("js.settings.account.pi_modal_cancelled"), ""));
      } else {
        cur.pillSlot.append(h.pill(t("js.settings.account.pi_modal_running"), "busy"));
      }
    }

    async function completeModalLogin(cur) {
      if (!cur || cur.closed || cur.busy) return;
      cur.busy = true;
      cur.errEl.textContent = "";
      cur.errEl.hidden = true;
      cur.verifyBtn.disabled = true;
      cur.cancelBtn.disabled = true;
      try {
        const res = await accountRequest("pi/login/complete", {attempt: cur.attempt});
        if (cur.closed) return;
        if (res?.state === "succeeded") {
          cur.running = false;
          updateModalPill(cur, "succeeded");
          invalidateChoices();
          await refreshStatus();
          teardownModal(true);
          return;
        }
        cur.errEl.textContent = h.errorText(res?.reason || "auth_not_ready");
        cur.errEl.hidden = false;
      } catch (error) {
        if (!cur.closed) {
          cur.errEl.textContent = h.errorText(error.code);
          cur.errEl.hidden = false;
        }
      } finally {
        if (!cur.closed) {
          cur.busy = false;
          cur.verifyBtn.disabled = false;
          cur.cancelBtn.disabled = false;
        }
      }
    }

    async function cancelModalLogin(cur) {
      if (!cur || cur.closed) return;
      const attempt = cur.attempt;
      const wasRunning = cur.running;
      teardownModal(true);
      if (wasRunning && attempt) {
        try {
          await accountRequest("pi/login/cancel", {attempt});
        } catch (_) {}
        try { await refreshStatus(); } catch (_) {}
      }
    }

    function pollExitOutcome(cur, triesLeft) {
      if (!cur || cur.closed) return;
      cur.statusPollTimer = window.setTimeout(async () => {
        cur.statusPollTimer = null;
        if (cur.closed) return;
        try {
          const st = await accountRequest("pi/login/status");
          if (cur.closed) return;
          if (st?.attempt === cur.attempt && st.state && st.state !== "running") {
            cur.running = false;
            updateModalPill(cur, st.state);
            if (st.state === "succeeded") {
              invalidateChoices();
              await refreshStatus();
              teardownModal(true);
              return;
            }
            if (st.state === "failed") {
              cur.errEl.textContent = h.errorText(st.reason, "js.settings.account.slot_login_failed");
              cur.errEl.hidden = false;
              cur.verifyBtn.disabled = true;
              await refreshStatus();
              return;
            }
            await refreshStatus();
            teardownModal(true);
            return;
          }
        } catch (_) {}
        if (triesLeft > 1 && !cur.closed) pollExitOutcome(cur, triesLeft - 1);
      }, 250);
    }

    async function openPtyModal(started, opener) {
      if (modal) teardownModal(false);

      const backdrop = el("div", "acc-pty-backdrop");
      backdrop.dataset.piPtyModal = "1";
      const dialog = el("div", "acc-pty-modal");
      dialog.setAttribute("role", "dialog");
      dialog.setAttribute("aria-modal", "true");
      dialog.setAttribute("aria-labelledby", "acc-pty-modal-title");

      const head = el("div", "acc-pty-head");
      const title = el("h3", "acc-pty-title", t("js.settings.account.pi_modal_title"));
      title.id = "acc-pty-modal-title";
      const slotTag = el("span", "acc-pty-slot", started.label || started.slot_id || "");
      const pillSlot = el("span", "");
      const closeBtn = el("button", "acc-pty-close", "×");
      closeBtn.type = "button";
      closeBtn.setAttribute("aria-label", t("js.settings.account.cancel"));
      head.append(title, slotTag, pillSlot, el("span", "acc-pty-sp"), closeBtn);

      const hintKey = started.provider ? "js.settings.account.pi_modal_hint_provider" : "js.settings.account.pi_modal_hint";
      const hint = el("p", "acc-pty-hint", t(hintKey));
      const termHost = el("div", "acc-pty-term");
      const loadingEl = el("div", "acc-pty-loading", t("js.settings.account.pi_modal_loading"));
      termHost.append(loadingEl);

      const foot = el("div", "acc-pty-foot");
      const errEl = el("p", "acc-pty-err");
      errEl.setAttribute("role", "alert");
      errEl.hidden = true;
      const verifyBtn = h.ab(t("js.settings.account.pi_modal_verify"), null, {cls: "solid"});
      verifyBtn.dataset.piModalComplete = "1";
      const cancelBtn = h.ab(t("js.settings.account.cancel"), null);
      cancelBtn.dataset.piModalCancel = "1";
      foot.append(errEl, append(el("div", "acc-pty-acts"), cancelBtn, verifyBtn));

      dialog.append(head, hint, termHost, foot);
      backdrop.append(dialog);
      document.body.append(backdrop);

      const cur = {
        attempt: started.attempt,
        slotId: started.slot_id,
        wsPath: started.ws_path,
        running: true,
        closed: false,
        busy: false,
        opener,
        backdrop,
        dialog,
        pillSlot,
        termHost,
        errEl,
        verifyBtn,
        cancelBtn,
        term: null,
        fitAddon: null,
        ws: null,
        resizeObserver: null,
        fitTimer: null,
        statusPollTimer: null,
        themeObserver: null,
        mediaQuery: null,
        onMediaChange: null,
        onKeyDown: null
      };
      modal = cur;
      updateModalPill(cur, "running");

      verifyBtn.addEventListener("click", () => completeModalLogin(cur));
      cancelBtn.addEventListener("click", () => cancelModalLogin(cur));
      closeBtn.addEventListener("click", () => cancelModalLogin(cur));
      cur.onKeyDown = ev => {
        if (ev.key === "Escape") {
          ev.preventDefault();
          ev.stopPropagation();
          cancelModalLogin(cur);
        }
      };
      document.addEventListener("keydown", cur.onKeyDown, true);

      try {
        await loadXterm();
        if (cur.closed) return;
        loadingEl.remove();

        const TerminalCtor = window.Terminal;
        const FitCtor = window.FitAddon?.FitAddon || window.FitAddon;
        const term = new TerminalCtor({
          cursorBlink: true,
          fontSize: 13,
          fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, "' + SYMBOLS_FONT + '", monospace',
          scrollback: 2000,
          theme: buildTermTheme()
        });
        const fitAddon = typeof FitCtor === "function" ? new FitCtor() : null;
        if (fitAddon && typeof term.loadAddon === "function") term.loadAddon(fitAddon);
        if (typeof term.open === "function") term.open(termHost);
        if (fitAddon && typeof fitAddon.fit === "function") {
          try { fitAddon.fit(); } catch (_) {}
        }
        cur.term = term;
        cur.fitAddon = fitAddon;

        const applyTheme = () => {
          if (cur.term && cur.term.options) cur.term.options.theme = buildTermTheme();
        };
        if (typeof window.MutationObserver === "function" && document.documentElement) {
          cur.themeObserver = new window.MutationObserver(applyTheme);
          cur.themeObserver.observe(document.documentElement, {attributes: true, attributeFilter: ["data-theme"]});
        }
        if (typeof window.matchMedia === "function") {
          const mq = window.matchMedia("(prefers-color-scheme: dark)");
          if (mq && typeof mq.addEventListener === "function") {
            cur.mediaQuery = mq;
            cur.onMediaChange = applyTheme;
            mq.addEventListener("change", applyTheme);
          }
        }

        const wsSend = payload => {
          if (cur.ws && cur.ws.readyState === 1) {
            try { cur.ws.send(JSON.stringify(payload)); } catch (_) {}
          }
        };
        const sendSize = () => {
          if (!cur.term) return;
          const rows = cur.term.rows || 24;
          const cols = cur.term.cols || 80;
          wsSend({t: "size", rows, cols});
        };

        if (typeof term.onData === "function") {
          term.onData(data => wsSend({t: "in", data}));
        }
        if (typeof window.ResizeObserver === "function") {
          cur.resizeObserver = new window.ResizeObserver(() => {
            if (cur.closed) return;
            if (cur.fitTimer !== null) window.clearTimeout(cur.fitTimer);
            cur.fitTimer = window.setTimeout(() => {
              cur.fitTimer = null;
              if (cur.closed) return;
              if (cur.fitAddon && typeof cur.fitAddon.fit === "function") {
                try { cur.fitAddon.fit(); } catch (_) {}
              }
              sendSize();
            }, FIT_DEBOUNCE_MS);
          });
          cur.resizeObserver.observe(termHost);
        }

        const WS = window.WebSocket;
        if (typeof WS !== "function") throw new Error("ws_unavailable");
        const ws = new WS(buildWsUrl(started.ws_path));
        cur.ws = ws;
        ws.onopen = () => {
          if (cur.closed) return;
          sendSize();
          if (typeof term.focus === "function") term.focus();
        };
        ws.onmessage = ev => {
          if (cur.closed) return;
          let frame = null;
          try { frame = JSON.parse(ev.data); } catch (_) { return; }
          if (!frame || typeof frame !== "object") return;
          if (frame.t === "hello") {
            if (frame.state && frame.state !== "running") pollExitOutcome(cur, 6);
            sendSize();
          } else if (frame.t === "out" && frame.data) {
            if (typeof term.write === "function") term.write(decodeBase64Bytes(frame.data));
          } else if (frame.t === "gap") {
            if (typeof term.writeln === "function") term.writeln("\r\n[" + t("js.settings.account.pi_modal_gap") + "]");
          } else if (frame.t === "ping") {
            wsSend({t: "pong"});
          } else if (frame.t === "exit" || frame.t === "closed") {
            pollExitOutcome(cur, 8);
          }
        };
        ws.onerror = () => {
          if (!cur.closed && cur.running) {
            cur.errEl.textContent = t("js.settings.account.request_failed");
            cur.errEl.hidden = false;
          }
        };
      } catch (error) {
        if (!cur.closed) {
          loadingEl.remove();
          cur.errEl.textContent = h.errorText(error?.code, "js.settings.account.pi_pty_unavailable");
          cur.errEl.hidden = false;
        }
      }
      render();
    }

    async function startLogin(opts, opener) {
      if (slotBusy || runtimeBusy || isLoginRunning()) return;
      slotBusy = true;
      slotError = "";
      render();
      try {
        const body = {
          existing: !!opts.existing,
          rows: 24,
          cols: 80
        };
        if (opts.slot_id) body.slot_id = opts.slot_id;
        if (opts.label) body.label = opts.label;
        if (opts.provider) body.provider = opts.provider;
        const started = await accountRequest("pi/login/start", body);
        addDrawerOpen = false;
        addDraft = {label: "", provider: ""};
        slotBusy = false;
        await openPtyModal(started, opener);
      } catch (error) {
        slotError = h.errorText(error.code);
        slotBusy = false;
        render();
      }
    }

    function phaseLabel(phase) {
      if (phase === "verify_manifest") return t("js.settings.account.pi_phase_verify");
      if (phase === "extract") return t("js.settings.account.pi_phase_extract");
      return t("js.settings.account.pi_phase_download");
    }

    function renderRuntimeBlock(rt, isLoopback) {
      const installing = !!rt.installing;
      const installed = !!rt.installed;
      const loginActive = isLoginRunning();
      const versionTag = rt.version ? "v" + rt.version : "";

      const block = h.providerBlock({
        rowName: "pi-runtime",
        iconClass: "ic-pirpc",
        monogram: "Pi",
        title: "Pi",
        tag: versionTag,
        subtitle: t("js.settings.account.pi_runtime_subtitle")
      });

      if (installing) {
        block.acts.append(h.busyPill("js.settings.account.pi_installing"));
      } else if (installed) {
        block.acts.append(h.pill(t("js.settings.account.pi_installed"), "ok", "", true));
      } else {
        block.acts.append(h.pill(t("js.settings.account.pi_not_installed"), ""));
      }

      const installBtn = h.ab(
        t(installed ? "js.settings.account.pi_reinstall" : "js.settings.account.pi_install"),
        installRuntime,
        {cls: installed ? "" : "solid", disabled: runtimeBusy || installing || loginActive || !isLoopback}
      );
      installBtn.dataset.piRuntimeInstall = "1";
      block.acts.append(installBtn);

      if (installed) {
        const uninstallBtn = h.holdBar(
          t("js.settings.account.pi_uninstall"),
          t("js.settings.account.pi_uninstall_aria"),
          uninstallRuntime,
          {disabled: runtimeBusy || installing || loginActive || !isLoopback}
        );
        uninstallBtn.dataset.piRuntimeUninstall = "1";
        block.acts.append(uninstallBtn);
      }

      if (installing) {
        const prog = rt.progress || {};
        const total = Number(prog.total) || 0;
        const downloaded = Number(prog.downloaded) || 0;
        const wrap = el("div", "acc-pi-prog-wrap");
        const head = el("div", "acc-pi-prog-head");
        head.append(el("span", "acc-pi-prog-phase", phaseLabel(prog.phase)));
        if (total > 0) {
          head.append(el("span", "acc-pi-prog-bytes", formatMegabytes(downloaded) + " / " + formatMegabytes(total) + " MB"));
        }
        const track = el("div", "acc-pi-prog");
        const bar = el("div", "acc-pi-prog-bar" + (total <= 0 ? " is-indeterminate" : ""));
        if (total > 0) {
          const ratio = Math.max(0, Math.min(1, downloaded / total));
          bar.style.setProperty("--p", ratio.toFixed(4));
        }
        track.append(bar);
        wrap.append(head, track);
        block.kids.append(wrap);
      }

      const errMsg = runtimeError || rt.error || "";
      h.setNotice(block.noticeSlot, errMsg, errMsg ? "fail" : "");
      return block.root;
    }

    function renderAccountsBlock(rt, slots, isLoopback) {
      const installed = !!rt.installed;
      const installing = !!rt.installing;
      const loginActive = isLoginRunning();
      const canPty = installed && !!termScriptName() && isLoopback;

      const block = h.providerBlock({
        rowName: "slots-pi-rpc",
        iconClass: "ic-pirpc",
        monogram: "Pi",
        title: t("js.settings.account.pi_accounts_title"),
        subtitle: t("js.settings.account.pi_accounts_subtitle")
      });

      const addBtn = h.ab(
        t("js.settings.account.pi_add_account"),
        () => {
          addDrawerOpen = !addDrawerOpen;
          slotError = "";
          render();
          if (addDrawerOpen) {
            const inp = group.body.querySelector("[data-pi-login-label]");
            if (inp && typeof inp.focus === "function") inp.focus();
          }
        },
        {cls: "pri", disabled: !canPty || installing || loginActive || slotBusy}
      );
      addBtn.dataset.piAddAccount = "1";
      block.acts.append(addBtn);

      if (renameState && !slots.some(s => s.id === renameState.slotId)) {
        renameState.form.remove();
        renameState = null;
      }

      for (const slot of slots) {
        const isRenaming = !!(renameState && renameState.slotId === slot.id);
        const label = slot.id === "default" ? t("js.settings.account.slot_default") : (slot.label || slot.id);
        const row = el("div", "acc-row" + (slot.active ? " is-active" : ""));
        row.dataset.slotId = slot.id;

        const lead = el("div", "acc-lead");
        if (slot.active) {
          lead.append(h.pill(t("js.settings.account.active"), "on", "", true));
        } else {
          const swBtn = h.ab(
            t("js.settings.account.use_account"),
            () => mutateSlot("slots/switch", {harness: HARNESS, slot_id: slot.id}),
            {cls: "pri", disabled: slotBusy}
          );
          swBtn.dataset.slotSwitch = slot.id;
          lead.append(swBtn);
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
        const manageBtn = h.ab(
          t("js.settings.account.pi_slot_manage_auth"),
          () => startLogin({existing: true, slot_id: slot.id, label}, manageBtn),
          {disabled: !canPty || installing || loginActive || slotBusy}
        );
        manageBtn.dataset.piSlotLogin = slot.id;
        acts.append(manageBtn);

        if (slot.id !== "default") {
          if (!isRenaming) {
            const rnBtn = h.ab(t("js.settings.account.slot_rename"), () => openRename(slot), {disabled: slotBusy});
            rnBtn.dataset.slotRename = slot.id;
            acts.append(rnBtn);
          }
          const busyTarget = loginActive && (modal?.slotId === slot.id || loginSnapshot()?.slot_id === slot.id);
          const rmBtn = h.holdBar(
            t("js.settings.account.remove_btn"),
            t("js.settings.account.pi_slot_remove_aria", label),
            () => mutateSlot("slots/remove", {harness: HARNESS, slot_id: slot.id, delete_dir: !!slot.managed}),
            {disabled: slotBusy || busyTarget}
          );
          rmBtn.dataset.slotRemove = slot.id;
          acts.append(rmBtn);
        }

        row.append(lead, main, acts);
        block.kids.append(row);
      }

      if (addDrawerOpen && canPty) {
        const drawer = el("form", "acc-drawer account-form");
        drawer.dataset.piAddDrawer = "1";
        drawer.autocomplete = "off";

        const labelInp = el("input", "inp acc-inp");
        labelInp.type = "text";
        labelInp.id = "account-pi-login-label";
        labelInp.maxLength = 80;
        labelInp.placeholder = t("js.settings.account.pi_login_label_placeholder");
        labelInp.value = addDraft.label;
        labelInp.dataset.piLoginLabel = "1";
        labelInp.addEventListener("input", () => { addDraft.label = labelInp.value; });

        const provInp = el("input", "inp mono acc-inp");
        provInp.type = "text";
        provInp.id = "account-pi-login-provider";
        provInp.maxLength = 64;
        provInp.spellcheck = false;
        provInp.placeholder = t("js.settings.account.pi_login_provider_placeholder");
        provInp.value = addDraft.provider;
        provInp.dataset.piLoginProvider = "1";
        provInp.addEventListener("input", () => { addDraft.provider = provInp.value; });

        const launchBtn = h.ab(t("js.settings.account.pi_login_launch"), null, {cls: "solid", submit: true, disabled: slotBusy || loginActive});
        const cancelBtn = h.ab(t("js.settings.account.cancel"), () => {
          addDrawerOpen = false;
          addDraft = {label: "", provider: ""};
          render();
        });
        drawer.addEventListener("submit", ev => {
          ev.preventDefault();
          startLogin({
            existing: false,
            label: labelInp.value.trim(),
            provider: provInp.value.trim()
          }, addBtn);
        });

        drawer.append(
          h.field(t("js.settings.account.pi_login_label"), labelInp),
          h.field(t("js.settings.account.pi_login_provider"), provInp, h.message(t("js.settings.account.pi_login_provider_help"))),
          append(el("div", "account-actions"), launchBtn, cancelBtn)
        );
        block.kids.append(drawer);
      }

      h.setNotice(block.noticeSlot, slotError, slotError ? "fail" : "");
      return block.root;
    }

    function render() {
      const rt = runtimeState();
      if (!rt) {
        group.root.hidden = true;
        group.body.replaceChildren();
        return;
      }
      group.root.hidden = false;
      const isLoopback = nav.status?.loopback_peer !== false;
      const piSlots = Array.isArray(nav.status?.slots?.[HARNESS]) ? nav.status.slots[HARNESS] : [];

      const savedFocus = h.captureFocus(group.body);
      const nodes = [renderRuntimeBlock(rt, isLoopback)];
      if (rt.installed || piSlots.length > 0) {
        nodes.push(renderAccountsBlock(rt, piSlots, isLoopback));
      }
      group.body.replaceChildren(...nodes);
      h.restoreFocus(group.body, savedFocus);
    }

    function applyStatus(before, result) {
      const prevRt = before?.pi_runtime || null;
      const nextRt = result?.pi_runtime || null;
      if (prevRt?.installing && nextRt && !nextRt.installing) {
        invalidateChoices();
      }
      if (nextRt?.installing) scheduleInstallPoll();
      else clearInstallPoll();

      const prevSlots = before?.slots?.[HARNESS];
      const nextSlots = result?.slots?.[HARNESS];
      if (JSON.stringify(prevRt) !== JSON.stringify(nextRt) ||
          JSON.stringify(before?.pi_login) !== JSON.stringify(result?.pi_login) ||
          JSON.stringify(prevSlots) !== JSON.stringify(nextSlots) ||
          before?.loopback_peer !== result?.loopback_peer) {
        render();
      }
    }

    function focus(provider) {
      if (provider !== HARNESS && provider !== "pi" && provider !== "pi-runtime") return false;
      if (group.root.hidden) return false;
      const target = group.body.querySelector('[data-account-row="slots-pi-rpc"]') ||
        group.body.querySelector('[data-account-row="pi-runtime"]');
      if (!target) return false;
      target.focus({preventScroll: true});
      if (typeof target.scrollIntoView === "function") target.scrollIntoView({block: "nearest"});
      return true;
    }

    return {
      element: group.root,
      render,
      applyStatus,
      focus,
      closeEditors() {
        addDrawerOpen = false;
        addDraft = {label: "", provider: ""};
        cancelRename(false);
        if (modal) cancelModalLogin(modal);
        render();
      },
      destroy() {
        destroyed = true;
        clearInstallPoll();
        if (modal) teardownModal(false);
      }
    };
  }

  window.WsSettingsAccountPiRPC = {create};
}());
