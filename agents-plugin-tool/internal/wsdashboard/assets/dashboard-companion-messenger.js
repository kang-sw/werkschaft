(function (global) {
  "use strict";

  // ============================================================================
  // Floating KakaoTalk-Style Companion Messenger Popup
  // ============================================================================
  // Mounted directly under document.body (position: fixed) so .rail overflow
  // never clips it and Variant D resizable rail widths (220px..360px+) anchor
  // cleanly beside #rail-companion-slot.
  // ============================================================================

  const POPUP_DEFAULT_W = 360;
  const POPUP_DEFAULT_H = 540;

  function mdApi() {
    return global.WsCompanionMessengerMd || global.WsCompanion?.messengerMd || {};
  }

  function makeSvg(doc, nameOrPaths) {
    const api = mdApi();
    const paths = typeof nameOrPaths === "string" ? (api.ICONS?.[nameOrPaths] || []) : nameOrPaths;
    return api.makeSvg(doc, paths);
  }

  function clamp(v, lo, hi) {
    return Math.min(Math.max(v, lo), hi);
  }

  function t(key, fallback, ...args) {
    let text = fallback;
    if (typeof global.wsT === "function") {
      const resolved = global.wsT(key, ...args);
      if (resolved && resolved !== key) text = resolved;
    }
    if (args.length && text === fallback) {
      let idx = 0;
      text = text.replace(/%[sd]/g, () => String(args[idx++] ?? ""));
    }
    return text;
  }

  function computePopupRect(anchorRect, viewport, popupSize) {
    return mdApi().computePopupRect(anchorRect, viewport, popupSize);
  }

  function formatTime(ms) { return mdApi().formatTime ? mdApi().formatTime(ms) : ""; }
  function statusForSnapshot(snap) { return mdApi().statusForSnapshot(snap, t); }
  function toneMeta(tone) { return mdApi().toneMeta ? mdApi().toneMeta(tone) : null; }
  function patchChildren(parent, next) { return mdApi().patchChildren(parent, next); }

  function renderMarkdownInto(doc, container, text) {
    const md = mdApi();
    if (typeof md.renderMarkdownInto === "function") {
      md.renderMarkdownInto(doc, container, text);
      return;
    }
    container.textContent = String(text == null ? "" : text);
  }

  function createMessengerController() {
    let mounted = false;
    let opts = null;
    let cancel = null;
    let openState = false;
    let pinned = false;
    let tier1Open = false;
    let sending = false;
    let fetchedOnce = false;
    let snapshot = {
      seq: 0,
      state: "laptop_ambient",
      posture: "laptop",
      phoneFaceDown: false,
      pendingApproval: null,
      messages: [],
      unreadClient: 0,
      unreadAgent: 0,
      tier1Summary: [],
      rotationMeta: { turns: 0, maxTurns: 20, summaryCap: 10 }
    };
    const localCardStatus = new Map();

    let brainMode = false;
    let brainInfo = null;
    let brainError = "";
    let brainLoading = false;
    let mountedBrainRid = "";

    let popup = null, caret = null, statusDot = null, nameEl = null, subtitleEl = null;
    let popoutBtn = null, tier1Btn = null, tier1Dot = null, pinBtn = null, settingsBtn = null, closeBtn = null;
    let tier1Strip = null, tier1Metrics = null, tier1SummaryEl = null, tier1ResetBtn = null;
    let brainWrap = null, brainFrame = null, brainFallbackEl = null, brainFallbackMsg = null, brainRetryBtn = null;
    let threadEl = null, typingEl = null, typingLabel = null, composerEl = null;
    let inputEl = null, sendBtn = null;
    // WsInput instance owning inputEl (null in the plain-textarea fallback)
    // and per-key rendered thread nodes.
    let inputApi = null;
    const nodeCache = new Map();

    function docRef() {
      return opts?.document || (typeof document !== "undefined" ? document : null);
    }

    function fetchImpl() {
      return opts?.fetch || (typeof fetch === "function" ? fetch.bind(global) : null);
    }

    function buildDom(doc) {
      popup = doc.createElement("section");
      popup.id = "companion-messenger";
      popup.className = "ws-cm-popup is-closed";
      popup.setAttribute("role", "dialog");
      popup.setAttribute("aria-modal", "false");
      popup.setAttribute("aria-hidden", "true");
      popup.setAttribute("aria-label", t("js.companion.messenger.title", "Companion"));

      caret = doc.createElement("div");
      caret.className = "ws-cm-caret";
      caret.setAttribute("aria-hidden", "true");

      // Header
      const header = doc.createElement("header");
      header.className = "ws-cm-header";

      const avatarWrap = doc.createElement("div");
      avatarWrap.className = "ws-cm-avatar";
      avatarWrap.append(makeSvg(doc, "avatar"));
      statusDot = doc.createElement("span");
      statusDot.className = "ws-cm-status-dot";
      statusDot.dataset.state = "laptop";
      avatarWrap.append(statusDot);

      const meta = doc.createElement("div");
      meta.className = "ws-cm-head-meta";
      const nameRow = doc.createElement("div");
      nameRow.className = "ws-cm-name-row";
      nameEl = doc.createElement("strong");
      nameEl.className = "ws-cm-name";
      nameEl.textContent = t("js.companion.messenger.title", "Companion");
      const rolePill = doc.createElement("span");
      rolePill.className = "ws-cm-role-pill";
      rolePill.textContent = "Co-Lead";
      nameRow.append(nameEl, rolePill);
      subtitleEl = doc.createElement("div");
      subtitleEl.className = "ws-cm-subtitle";
      meta.append(nameRow, subtitleEl);

      const actions = doc.createElement("div");
      actions.className = "ws-cm-head-actions";

      popoutBtn = doc.createElement("button");
      popoutBtn.type = "button";
      popoutBtn.className = "ws-cm-ibtn";
      popoutBtn.dataset.action = "open-full-tab";
      popoutBtn.title = t("js.companion.messenger.open_full_tab", "Open in full tab");
      popoutBtn.setAttribute("aria-label", popoutBtn.title);
      popoutBtn.hidden = true;
      popoutBtn.append(makeSvg(doc, "external"));

      tier1Btn = doc.createElement("button");
      tier1Btn.type = "button";
      tier1Btn.className = "ws-cm-ibtn";
      tier1Btn.dataset.action = "toggle-tier1";
      tier1Btn.setAttribute("aria-pressed", "false");
      tier1Btn.setAttribute("aria-expanded", "false");
      tier1Btn.title = t("js.companion.messenger.tier1_toggle", "Tier-1 summary");
      tier1Btn.setAttribute("aria-label", tier1Btn.title);
      tier1Btn.append(makeSvg(doc, "layers"));
      tier1Dot = doc.createElement("span");
      tier1Dot.className = "ws-cm-ibtn-dot";
      tier1Dot.hidden = true;
      tier1Btn.append(tier1Dot);

      pinBtn = doc.createElement("button");
      pinBtn.type = "button";
      pinBtn.className = "ws-cm-ibtn";
      pinBtn.dataset.action = "toggle-pin";
      pinBtn.setAttribute("aria-pressed", "false");
      pinBtn.title = t("js.companion.messenger.pin", "Pin popup");
      pinBtn.setAttribute("aria-label", pinBtn.title);
      pinBtn.append(makeSvg(doc, "pin"));

      settingsBtn = doc.createElement("button");
      settingsBtn.type = "button";
      settingsBtn.className = "ws-cm-ibtn";
      settingsBtn.dataset.action = "open-settings";
      settingsBtn.title = t("js.companion.settings", "Companion settings");
      settingsBtn.setAttribute("aria-label", settingsBtn.title);
      settingsBtn.append(makeSvg(doc, "gear"));
      if (!opts?.openSettings) settingsBtn.hidden = true;

      closeBtn = doc.createElement("button");
      closeBtn.type = "button";
      closeBtn.className = "ws-cm-ibtn ws-cm-close";
      closeBtn.dataset.action = "close-messenger";
      closeBtn.title = t("js.companion.messenger.close", "Close messenger (Esc)");
      closeBtn.setAttribute("aria-label", closeBtn.title);
      closeBtn.append(makeSvg(doc, "close"));

      actions.append(popoutBtn, tier1Btn, pinBtn, settingsBtn, closeBtn);
      header.append(avatarWrap, meta, actions);

      // Compact Brain Frame Container (/s/<brainRid>?frame=1&compact=1)
      brainWrap = doc.createElement("div");
      brainWrap.className = "ws-cm-brain-wrap";
      brainWrap.hidden = true;

      brainFallbackEl = doc.createElement("div");
      brainFallbackEl.className = "ws-cm-brain-fallback";
      brainFallbackEl.setAttribute("role", "status");
      brainFallbackEl.hidden = true;
      brainFallbackMsg = doc.createElement("span");
      brainFallbackMsg.className = "ws-cm-brain-fallback-msg";
      brainRetryBtn = doc.createElement("button");
      brainRetryBtn.type = "button";
      brainRetryBtn.className = "ws-cm-brain-retry";
      brainRetryBtn.dataset.action = "retry-brain";
      brainRetryBtn.textContent = t("js.companion.messenger.brain_retry", "Retry");
      brainFallbackEl.append(brainFallbackMsg, brainRetryBtn);

      // Tier-1 BoundedRotation Summary Strip
      tier1Strip = doc.createElement("div");
      tier1Strip.className = "ws-cm-tier1-strip";
      tier1Strip.hidden = true;
      const t1Head = doc.createElement("div");
      t1Head.className = "ws-cm-tier1-head";
      const t1Badge = doc.createElement("span");
      t1Badge.className = "ws-cm-tier1-badge";
      t1Badge.textContent = "Tier-1 · BoundedRotation";
      tier1Metrics = doc.createElement("span");
      tier1Metrics.className = "ws-cm-tier1-metrics";
      tier1ResetBtn = doc.createElement("button");
      tier1ResetBtn.type = "button";
      tier1ResetBtn.className = "ws-cm-tier1-reset";
      tier1ResetBtn.textContent = t("js.companion.messenger.tier1_reset", "Reset");
      t1Head.append(t1Badge, tier1Metrics, tier1ResetBtn);
      tier1SummaryEl = doc.createElement("div");
      tier1SummaryEl.className = "ws-cm-tier1-summary";
      tier1Strip.append(t1Head, tier1SummaryEl);

      // Scrollable Thread
      threadEl = doc.createElement("div");
      threadEl.className = "ws-cm-thread";
      threadEl.setAttribute("role", "log");
      threadEl.setAttribute("aria-live", "polite");

      // Typing / Choreography Indicator
      typingEl = doc.createElement("div");
      typingEl.className = "ws-cm-typing";
      typingEl.hidden = true;
      const dots = doc.createElement("span");
      dots.className = "ws-cm-typing-dots";
      dots.setAttribute("aria-hidden", "true");
      dots.append(doc.createElement("i"), doc.createElement("i"), doc.createElement("i"));
      typingLabel = doc.createElement("span");
      typingEl.append(dots, typingLabel);

      // Composer Footer
      composerEl = doc.createElement("footer");
      composerEl.className = "ws-cm-composer";
      const well = doc.createElement("div");
      well.className = "ws-cm-input-well";
      inputEl = doc.createElement("textarea");
      inputEl.className = "ws-cm-input";
      inputEl.rows = 1;
      inputEl.placeholder = t("js.companion.messenger.placeholder", "Message… (Enter to send)");
      inputEl.setAttribute("aria-label", inputEl.placeholder);
      sendBtn = doc.createElement("button");
      sendBtn.type = "button";
      sendBtn.className = "ws-cm-send-btn";
      sendBtn.disabled = true;
      sendBtn.title = t("js.companion.messenger.send", "Send message");
      sendBtn.setAttribute("aria-label", sendBtn.title);
      sendBtn.append(makeSvg(doc, "send"));
      well.append(inputEl, sendBtn);
      composerEl.append(well);

      popup.append(caret, header, brainFallbackEl, brainWrap, tier1Strip, threadEl, typingEl, composerEl);
      doc.body.append(popup);
    }

    function syncInputSize() {
      if (!inputEl) return;
      // With WsInput the visible field is the capsule editor, sized by CSS
      // (min/max-height + overflow); only the plain textarea needs a measure.
      if (!inputApi) {
        inputEl.style.height = "auto";
        const nextH = clamp(inputEl.scrollHeight || 28, 28, 92);
        inputEl.style.height = nextH + "px";
      }
      if (sendBtn) sendBtn.disabled = sending || !inputEl.value.trim();
    }

    // Attaches the shared WsInput primitive (capsule editor + live markdown
    // via WsMdEditor, list continuation, IME composition guard) to the
    // composer textarea. global.WsInput is resolved here, at mount time, so
    // script order never matters; a missing WsInput leaves the plain textarea
    // with its own Enter handler (see mount()).
    function attachWsInput() {
      const WsInput = global.WsInput;
      if (!inputEl || !WsInput || typeof WsInput.attach !== "function") return null;
      try {
        return WsInput.attach(inputEl, {
          // Messenger chat convention: Enter sends, Shift+Enter adds a line.
          keymap: () => "enter-send",
          features: {
            markdown: true,
            capsules: true,
            mentions: false,
            topics: false,
            slash: false,
            imagePaste: false,
            longPaste: false,
            listContinuation: true,
            bangMode: false,
            autoGrow: true
          },
          onChange: () => { syncInputSize(); },
          onSubmit: () => { sendMessage(); }
        });
      } catch (_) {
        return null;
      }
    }

    function resetComposer() {
      if (inputApi && typeof inputApi.setValue === "function") inputApi.setValue("");
      else if (inputEl) inputEl.value = "";
    }

    function reposition() {
      if (!popup || !mounted) return;
      const anchorRect = typeof opts?.getAnchorRect === "function" ? opts.getAnchorRect() : null;
      const vw = global.innerWidth || 1280;
      const vh = global.innerHeight || 800;
      const rect = computePopupRect(anchorRect, { width: vw, height: vh }, { width: POPUP_DEFAULT_W, height: POPUP_DEFAULT_H });
      popup.style.left = rect.left + "px";
      popup.style.top = rect.top + "px";
      popup.style.width = rect.width + "px";
      popup.style.height = rect.height + "px";
      if (caret) {
        caret.dataset.side = rect.caretSide;
        caret.style.bottom = rect.caretBottom + "px";
      }
    }

    function renderConfirmCard(doc, card) {
      return mdApi().renderConfirmCard(doc, card, localCardStatus, t, render);
    }

    function render() {
      if (!popup || !mounted) return;
      const doc = docRef();
      const st = statusForSnapshot(snapshot);
      if (statusDot) statusDot.dataset.state = st.dot;
      if (subtitleEl) subtitleEl.textContent = st.subtitle;

      const summaries = Array.isArray(snapshot.tier1Summary) ? snapshot.tier1Summary : [];
      if (tier1Dot) tier1Dot.hidden = summaries.length === 0;
      if (tier1Metrics) {
        const turns = snapshot.rotationMeta?.turns ?? 0;
        const maxTurns = snapshot.rotationMeta?.maxTurns ?? 20;
        tier1Metrics.textContent = t("js.companion.messenger.tier1_turns", "Turns %d/%d", turns, maxTurns);
      }
      if (tier1SummaryEl) {
        tier1SummaryEl.replaceChildren();
        if (summaries.length === 0) {
          const empty = doc.createElement("p");
          empty.className = "ws-cm-tier1-empty";
          empty.textContent = t("js.companion.messenger.tier1_empty", "No Tier-1 summary entries yet.");
          tier1SummaryEl.append(empty);
        } else {
          for (const line of summaries) {
            const p = doc.createElement("p");
            p.className = "ws-cm-tier1-line";
            p.textContent = "• " + String(line);
            tier1SummaryEl.append(p);
          }
        }
      }

      if (typingEl && typingLabel) {
        typingEl.hidden = !st.typing;
        typingLabel.textContent = st.typing;
      }

      if (threadEl) {
        const wasNearBottom = (threadEl.scrollHeight - threadEl.scrollTop - threadEl.clientHeight) < 48;
        const msgs = Array.isArray(snapshot.messages) ? snapshot.messages : [];
        const pendingApproval = snapshot.pendingApproval && snapshot.pendingApproval.requestId ? snapshot.pendingApproval : null;
        // Patch by key: each row (and the empty/echo blocks) is cached with a
        // signature of everything it displays. A typing/state-only snapshot
        // re-uses the same nodes, so unchanged bubbles are neither re-parsed
        // as Markdown nor re-inserted (which would replay the entry animation).
        const used = new Set();
        const keyed = (key, sig, build) => {
          used.add(key);
          const hit = nodeCache.get(key);
          if (hit && hit.sig === sig) return hit.node;
          const node = build();
          nodeCache.set(key, { sig, node });
          return node;
        };
        const nextNodes = [];
        if (msgs.length === 0 && !pendingApproval) {
          nextNodes.push(keyed("empty", "empty", () => {
            const empty = doc.createElement("div");
            empty.className = "ws-cm-empty";
            empty.textContent = t("js.companion.messenger.empty", "Send a quick note — it buzzes your companion's desk smartphone.");
            return empty;
          }));
        } else {
          let prevSender = "";
          msgs.forEach((msg, idx) => {
            const isUser = msg.sender === "user";
            const isCont = prevSender === msg.sender;
            prevSender = msg.sender;
            const card = msg.confirmCard && typeof msg.confirmCard === "object" ? msg.confirmCard : null;
            const sig = JSON.stringify([
              msg.sender, isCont, msg.text || "", msg.tone || "", msg.createdAt || 0,
              card ? [card, localCardStatus.get(card.id) || ""] : null
            ]);
            nextNodes.push(keyed("m:" + (msg.id || "#" + idx), sig, () => {
              const row = doc.createElement("div");
              row.className = "ws-cm-msg " + (isUser ? "user" : "ai") + (isCont ? " is-cont" : "");

              if (!isUser) {
                const av = doc.createElement("div");
                av.className = "ws-cm-msg-av" + (isCont ? " is-cont" : "");
                if (!isCont) av.append(makeSvg(doc, "avatar"));
                row.append(av);
              }

              const col = doc.createElement("div");
              col.className = "ws-cm-msg-col";
              if (!isUser && !isCont) {
                const sender = doc.createElement("span");
                sender.className = "ws-cm-sender";
                sender.textContent = t("js.companion.messenger.title", "Companion");
                col.append(sender);
              }

              const line = doc.createElement("div");
              line.className = "ws-cm-bubble-line";
              const bubble = doc.createElement("div");
              bubble.className = "ws-cm-bubble ws-cm-md";
              renderMarkdownInto(doc, bubble, msg.text || "");
              const time = doc.createElement("time");
              time.className = "ws-cm-time";
              time.textContent = formatTime(msg.createdAt);
              line.append(bubble, time);
              col.append(line);

              const tm = !isUser ? toneMeta(msg.tone) : null;
              if (tm) {
                const pills = doc.createElement("div");
                pills.className = "ws-cm-meta-pills";
                const pill = doc.createElement("span");
                pill.className = "ws-cm-react-pill" + (tm.cls ? " " + tm.cls : "");
                pill.textContent = tm.label;
                pills.append(pill);
                col.append(pills);
              }
              if (card) col.append(renderConfirmCard(doc, card));
              row.append(col);
              return row;
            }));
          });
        }
        if (pendingApproval) {
          const echoSig = JSON.stringify([pendingApproval.requestId, pendingApproval.tool || "", pendingApproval.summary || ""]);
          nextNodes.push(keyed("echo", echoSig, () => {
          const echo = doc.createElement("div");
          echo.className = "ws-cm-pause-echo";
          echo.setAttribute("role", "status");
          echo.setAttribute("data-request-id", pendingApproval.requestId);

          const head = doc.createElement("div");
          head.className = "ws-cm-pause-head";
          head.append(makeSvg(doc, "shield"));
          const title = doc.createElement("span");
          title.className = "ws-cm-pause-title";
          title.textContent = t("js.companion.messenger.approval_paused_title", "Waiting for 1-click approval");
          head.append(title);
          if (pendingApproval.tool) {
            const tool = doc.createElement("span");
            tool.className = "ws-cm-pause-tool";
            tool.textContent = pendingApproval.tool;
            head.append(tool);
          }
          echo.append(head);

          if (pendingApproval.summary) {
            const sum = doc.createElement("p");
            sum.className = "ws-cm-pause-summary";
            sum.textContent = pendingApproval.summary;
            echo.append(sum);
          }
          const hint = doc.createElement("p");
          hint.className = "ws-cm-pause-hint";
          hint.textContent = t("js.companion.messenger.approval_paused_hint", "Approve or deny in the top confirmation banner to continue.");
          echo.append(hint);
          return echo;
          }));
        }
        for (const key of [...nodeCache.keys()]) {
          if (!used.has(key)) nodeCache.delete(key);
        }
        patchChildren(threadEl, nextNodes);
        if (wasNearBottom || msgs.length <= 2 || Boolean(pendingApproval)) {
          threadEl.scrollTop = threadEl.scrollHeight;
        }
      }
      syncInputSize();
    }

    async function fetchThread() {
      const f = fetchImpl();
      if (!f) return;
      try {
        const res = await f("/api/companion/phone/thread", { credentials: "same-origin" });
        if (!res || !res.ok) return;
        const data = await res.json();
        fetchedOnce = true;
        applySnapshot(data);
        opts?.onSnapshot?.(data);
      } catch (_) { /* best effort */ }
    }

    async function markRead() {
      const f = fetchImpl();
      if (!f) return;
      try {
        const res = await f("/api/companion/phone/mark-read", {
          method: "POST",
          credentials: "same-origin",
          headers: { "Content-Type": "application/json" },
          body: "{}"
        });
        if (res && res.ok) {
          const data = await res.json();
          applySnapshot(data);
          opts?.onSnapshot?.(data);
        }
      } catch (_) { /* best effort */ }
    }

    async function sendMessage() {
      if (!inputEl || sending) return;
      const text = inputEl.value.trim();
      if (!text) return;
      const f = fetchImpl();
      if (!f) return;
      sending = true;
      syncInputSize();
      try {
        const res = await f("/api/companion/phone/send", {
          method: "POST",
          credentials: "same-origin",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ text })
        });
        if (!res || !res.ok) {
          global.WsToast?.show?.(t("js.companion.messenger.send_failed", "Failed to send message."), { kind: "warn" });
          return;
        }
        const data = await res.json();
        resetComposer();
        // The server answers {message, thread}; `snapshot` is kept as a fallback.
        const next = data?.thread || data?.snapshot;
        if (next) {
          applySnapshot(next);
          opts?.onSnapshot?.(next);
        }
      } catch (_) {
        global.WsToast?.show?.(t("js.companion.messenger.send_failed", "Failed to send message."), { kind: "warn" });
      } finally {
        sending = false;
        syncInputSize();
        inputEl?.focus?.();
      }
    }

    async function resetSummary() {
      const f = fetchImpl();
      if (!f) return;
      try {
        const res = await f("/api/companion/phone/reset-summary", {
          method: "POST",
          credentials: "same-origin",
          headers: { "Content-Type": "application/json" },
          body: "{}"
        });
        if (res && res.ok) {
          const data = await res.json();
          applySnapshot(data);
          opts?.onSnapshot?.(data);
        }
      } catch (_) { /* best effort */ }
    }

    function applySnapshot(next) {
      if (!next || typeof next !== "object") return;
      // SSE events and POST responses race: drop a snapshot older than the
      // one shown (same daemon epoch, lower seq).
      const newer = global.WsCompanion?.phoneSnapNewer;
      if (typeof newer === "function" && !newer(snapshot, next)) return;
      const pendingApproval = next.pendingApproval && typeof next.pendingApproval === "object" && next.pendingApproval.requestId
        ? next.pendingApproval
        : null;
      snapshot = {
        ...snapshot,
        ...next,
        pendingApproval,
        messages: Array.isArray(next.messages) ? next.messages : snapshot.messages,
        tier1Summary: Array.isArray(next.tier1Summary) ? next.tier1Summary : snapshot.tier1Summary,
        rotationMeta: next.rotationMeta && typeof next.rotationMeta === "object" ? next.rotationMeta : snapshot.rotationMeta
      };
      render();
      if (openState && (snapshot.unreadClient || 0) > 0) {
        markRead();
      }
    }

    function currentOrigin() {
      try { return global.location?.origin || ""; } catch (_) { return ""; }
    }

    function currentTheme() {
      const doc = docRef();
      return doc?.documentElement?.getAttribute?.("data-theme") || "";
    }

    function postToBrainFrame(payload) {
      if (!brainFrame || !brainFrame.contentWindow || typeof brainFrame.contentWindow.postMessage !== "function") return;
      try {
        brainFrame.contentWindow.postMessage(payload, currentOrigin() || "*");
      } catch (_) { /* iframe unloading */ }
    }

    function syncTheme(theme) {
      const next = typeof theme === "string" ? theme : currentTheme();
      if (!brainFrame) return;
      postToBrainFrame({ wsShell: "theme", theme: next });
    }

    function syncBrainVisibility(shown) {
      if (!brainFrame) return;
      if (!shown && typeof brainFrame.contentWindow?.wsHideFrame === "function") {
        try { brainFrame.contentWindow.wsHideFrame(); } catch (_) { /* best effort */ }
      }
      postToBrainFrame({ wsShell: "shown", shown: !!shown });
      if (shown) syncTheme(currentTheme());
    }

    function mountBrainFrame(replyId) {
      const doc = docRef();
      if (!doc || !brainWrap || !replyId) return;
      const src = "/s/" + encodeURIComponent(replyId) + "?frame=1&compact=1";
      if (!brainFrame) {
        brainFrame = doc.createElement("iframe");
        brainFrame.className = "ws-cm-frame";
        brainFrame.setAttribute("title", t("js.companion.messenger.title", "Companion"));
        brainFrame.addEventListener("load", () => {
          syncTheme(currentTheme());
          syncBrainVisibility(openState);
        }, cancel ? { signal: cancel.signal } : undefined);
        brainWrap.replaceChildren(brainFrame);
      }
      if (mountedBrainRid !== replyId || brainFrame.getAttribute("src") !== src) {
        mountedBrainRid = replyId;
        brainFrame.src = src;
        brainFrame.setAttribute("src", src);
      }
    }

    function applyBrainView() {
      if (!popup || !mounted) return;
      const activeBrain = brainMode && !!(brainInfo && brainInfo.replyId);
      popup.dataset.mode = activeBrain ? "brain" : "legacy";
      if (brainWrap) brainWrap.hidden = !activeBrain;
      if (popoutBtn) popoutBtn.hidden = !activeBrain;
      if (tier1Btn) tier1Btn.hidden = activeBrain;
      if (tier1Strip && activeBrain) tier1Strip.hidden = true;
      if (threadEl) threadEl.hidden = activeBrain;
      if (typingEl && activeBrain) typingEl.hidden = true;
      if (composerEl) composerEl.hidden = activeBrain;
      if (brainFallbackEl) {
        brainFallbackEl.hidden = activeBrain || !brainError;
        if (brainFallbackMsg && brainError) brainFallbackMsg.textContent = brainError;
      }
      if (nameEl) {
        const persona = activeBrain && brainInfo.personaName ? String(brainInfo.personaName).trim() : "";
        nameEl.textContent = persona || t("js.companion.messenger.title", "Companion");
      }
      if (activeBrain && subtitleEl) {
        const parts = [];
        if (brainInfo.harness) parts.push(brainInfo.harness);
        if (brainInfo.model) parts.push(brainInfo.model);
        subtitleEl.textContent = parts.length ? parts.join(" · ") : t("js.companion.messenger.brain_ready", "Full Lead connected");
      }
    }

    async function ensureBrain() {
      const f = fetchImpl();
      if (!f || brainLoading) return;
      brainLoading = true;
      try {
        const res = await f("/api/companion/brain", { credentials: "same-origin" });
        if (!res) return;
        if (res.status === 404) {
          brainMode = false;
          brainError = "";
          applyBrainView();
          return;
        }
        let data = null;
        try { data = await res.json(); } catch (_) { data = null; }
        if (res.ok && data && data.ready && data.replyId) {
          const changed = mountedBrainRid && mountedBrainRid !== data.replyId;
          brainInfo = data;
          brainMode = true;
          brainError = "";
          mountBrainFrame(data.replyId);
          applyBrainView();
          if (openState && !changed) syncBrainVisibility(true);
          return;
        }
        if (data && (data.ready === false || data.error)) {
          brainMode = false;
          brainError = data.error || t("js.companion.messenger.brain_unavailable", "Companion Lead is unavailable — retry to reconnect.");
          applyBrainView();
        }
      } catch (_) {
        // Legacy daemons or mocked fetch environments stay cleanly in fallback mode.
      } finally {
        brainLoading = false;
      }
    }

    function onBrainChanged(payload) {
      if (!payload || typeof payload !== "object") return;
      const nextRid = typeof payload.replyId === "string" ? payload.replyId.trim() : "";
      if (!nextRid) return;
      if (!brainInfo) brainInfo = { replyId: nextRid, ready: true };
      else brainInfo = { ...brainInfo, replyId: nextRid, ready: true };
      if (brainMode || brainFrame) {
        brainMode = true;
        brainError = "";
        mountBrainFrame(nextRid);
        applyBrainView();
        if (openState) syncBrainVisibility(true);
      }
    }

    function open() {
      if (!mounted || !popup) return false;
      openState = true;
      reposition();
      popup.classList.remove("is-closed");
      popup.setAttribute("aria-hidden", "false");
      opts?.onOpenChange?.(true);
      ensureBrain();
      if (brainMode && brainFrame) {
        syncBrainVisibility(true);
      } else {
        if (!fetchedOnce) fetchThread();
        if ((snapshot.unreadClient || 0) > 0) markRead();
      }
      render();
      applyBrainView();
      if (!brainMode && threadEl) threadEl.scrollTop = threadEl.scrollHeight;
      if (!brainMode) inputEl?.focus?.();
      return true;
    }

    function close() {
      if (!mounted || !popup || !openState) return false;
      openState = false;
      popup.classList.add("is-closed");
      popup.setAttribute("aria-hidden", "true");
      if (brainMode && brainFrame) syncBrainVisibility(false);
      opts?.onOpenChange?.(false);
      return true;
    }

    function toggle() {
      return openState ? (close(), false) : open();
    }

    function mount(mountOptions) {
      if (mounted) dispose();
      opts = mountOptions || {};
      const doc = docRef();
      if (!doc || !doc.body) return;
      mounted = true;
      cancel = new AbortController();
      const sig = { signal: cancel.signal };
      buildDom(doc);

      popoutBtn.addEventListener("click", () => {
        if (!brainInfo || !brainInfo.replyId) return;
        global.open?.("/s/" + encodeURIComponent(brainInfo.replyId), "_blank", "noopener");
      }, sig);

      brainRetryBtn.addEventListener("click", () => {
        brainError = "";
        applyBrainView();
        ensureBrain();
      }, sig);

      tier1Btn.addEventListener("click", () => {
        tier1Open = !tier1Open;
        tier1Strip.hidden = !tier1Open;
        tier1Btn.setAttribute("aria-pressed", tier1Open ? "true" : "false");
        tier1Btn.setAttribute("aria-expanded", tier1Open ? "true" : "false");
      }, sig);

      pinBtn.addEventListener("click", () => {
        pinned = !pinned;
        pinBtn.setAttribute("aria-pressed", pinned ? "true" : "false");
      }, sig);

      settingsBtn.addEventListener("click", () => {
        if (!pinned) close();
        opts?.openSettings?.(settingsBtn);
      }, sig);

      closeBtn.addEventListener("click", () => close(), sig);
      tier1ResetBtn.addEventListener("click", () => resetSummary(), sig);
      sendBtn.addEventListener("click", () => sendMessage(), sig);

      // WsInput, when present, owns Enter (send) / Shift+Enter (newline), the
      // IME composition guard and list continuation; its onChange keeps the
      // send button state in sync. The plain textarea gets the same keys.
      inputApi = attachWsInput();
      if (!inputApi) {
        inputEl.addEventListener("input", syncInputSize, sig);
        inputEl.addEventListener("keydown", event => {
          if (event.key === "Enter" && !event.shiftKey && !event.isComposing && event.keyCode !== 229) {
            event.preventDefault();
            sendMessage();
          }
        }, sig);
      }

      doc.addEventListener("keydown", event => {
        if (event.key === "Escape" && openState && !pinned) {
          event.preventDefault();
          close();
          opts?.getChatButton?.()?.focus?.();
        }
      }, sig);

      doc.addEventListener("pointerdown", event => {
        if (!openState || pinned) return;
        const target = event.target;
        if (popup?.contains(target)) return;
        const btn = opts?.getChatButton?.();
        if (btn && (btn === target || btn.contains(target))) return;
        if (opts?.isToggleTarget?.(target, event)) return;
        close();
      }, sig);

      global.addEventListener?.("resize", reposition, sig);
      global.addEventListener?.("message", event => {
        if (!event || !brainFrame || !brainFrame.contentWindow) return;
        const origin = currentOrigin();
        if (origin && event.origin !== origin) return;
        if (event.source !== brainFrame.contentWindow) return;
        const data = event.data;
        if (!data || typeof data !== "object") return;
        if (data.wsCompanionCompact === "user-send") {
          global.WsCompanion?.reflex?.onUserSend?.(data);
          opts?.onUserSend?.(data);
        }
      }, sig);

      if (global.wsCompanionPhoneState && typeof global.wsCompanionPhoneState === "object") {
        applySnapshot(global.wsCompanionPhoneState);
      } else {
        render();
      }
      applyBrainView();
      reposition();
    }

    function dispose() {
      cancel?.abort();
      cancel = null;
      try { inputApi?.destroy?.(); } catch (_) { /* best effort */ }
      inputApi = null;
      nodeCache.clear();
      popup?.remove?.();
      popup = caret = statusDot = nameEl = subtitleEl = null;
      popoutBtn = tier1Btn = tier1Dot = pinBtn = settingsBtn = closeBtn = null;
      tier1Strip = tier1Metrics = tier1SummaryEl = tier1ResetBtn = null;
      brainWrap = brainFrame = brainFallbackEl = brainFallbackMsg = brainRetryBtn = null;
      threadEl = typingEl = typingLabel = composerEl = inputEl = sendBtn = null;
      brainMode = false;
      brainInfo = null;
      brainError = "";
      brainLoading = false;
      mountedBrainRid = "";
      openState = false;
      mounted = false;
    }

    return {
      mount,
      dispose,
      open,
      close,
      toggle,
      reposition,
      applySnapshot,
      syncTheme,
      onBrainChanged,
      isOpen() { return openState; },
      isPinned() { return pinned; },
      getState() { return { open: openState, pinned, tier1Open, brainMode, brainInfo, snapshot }; },
      computePopupRect
    };
  }

  const controller = createMessengerController();
  global.WsCompanionMessenger = controller;
  if (global.WsCompanion) {
    global.WsCompanion.messenger = controller;
    global.WsCompanion.computePopupRect = computePopupRect;
  }
})(window);
