  // --- top-level shell 1-click daemon confirmation banner --------------------
  // Renders mutating ws/daemon.* confirmation prompts in a dedicated top-level
  // shell slot ([data-daemon-confirm]) that never collides with the OpenAI
  // account consent banner ([data-openai-consent] / :root[data-account-banner]).
  // Anti-self-approval: the entire section carries data-ws-dom-exclude="1" and
  // every decision button requires a real browser event (event.isTrusted === true).
  var dcBanner = doc.querySelector("section[data-daemon-confirm]");
  var dcActive = null, dcQueue = [], dcResize = null, dcTimerId = 0;

  function setDaemonConfirmHidden(hidden) {
    if (!dcBanner) return;
    dcBanner.hidden = hidden;
    if (hidden) {
      root.removeAttribute("data-daemon-confirm");
      if (dcResize) dcResize.disconnect();
      root.style.removeProperty("--daemon-confirm-height");
    } else {
      root.setAttribute("data-daemon-confirm", "");
      var rect = dcBanner.getBoundingClientRect ? dcBanner.getBoundingClientRect() : null;
      if (rect && rect.height > 0) {
        root.style.setProperty("--daemon-confirm-height", Math.ceil(rect.height) + "px");
      }
      if (typeof ResizeObserver === "function") {
        dcResize = dcResize || new ResizeObserver(function (entries) {
          var entry = entries && entries[0];
          if (!entry || dcBanner.hidden) return;
          var size = entry.borderBoxSize && entry.borderBoxSize[0] ? entry.borderBoxSize[0].blockSize : entry.contentRect.height;
          if (size > 0) root.style.setProperty("--daemon-confirm-height", Math.ceil(size) + "px");
        });
        dcResize.observe(dcBanner, { box: "border-box" });
      }
    }
  }

  function stopDaemonConfirmTimer() {
    if (dcTimerId) {
      clearInterval(dcTimerId);
      dcTimerId = 0;
    }
  }

  function renderDaemonConfirm(req) {
    stopDaemonConfirmTimer();
    if (!dcBanner || !req) {
      dcActive = null;
      if (dcBanner) dcBanner.replaceChildren();
      setDaemonConfirmHidden(true);
      return;
    }
    dcActive = req;
    dcBanner.replaceChildren();
    dcBanner.setAttribute("data-request-id", req.request_id);

    var copy = doc.createElement("div");
    copy.className = "daemon-confirm-copy";
    var hd = doc.createElement("div");
    hd.className = "daemon-confirm-hd";
    var title = doc.createElement("strong");
    title.textContent = wsT("js.dc.confirm.title");
    hd.appendChild(title);

    var badgeText = "";
    if (req.method && req.path) badgeText = req.method + " " + req.path;
    else if (req.action) badgeText = req.action;
    else if (req.tool) badgeText = req.tool;
    if (badgeText) {
      var badge = doc.createElement("code");
      badge.className = "daemon-confirm-badge";
      badge.textContent = badgeText;
      hd.appendChild(badge);
    }

    var timerEl = doc.createElement("span");
    timerEl.className = "daemon-confirm-timer";
    var deadline = req.deadline || (Date.now() + (Number(req.expires_in_ms) > 0 ? Number(req.expires_in_ms) : 120000));
    function syncTimer() {
      var left = Math.max(0, Math.ceil((deadline - Date.now()) / 1000));
      timerEl.textContent = wsT("js.dc.confirm.expires", left);
      if (left <= 0) stopDaemonConfirmTimer();
    }
    syncTimer();
    dcTimerId = setInterval(syncTimer, 1000);
    hd.appendChild(timerEl);
    copy.appendChild(hd);

    var summaryParts = [];
    if (req.summary) summaryParts.push(String(req.summary));
    else {
      if (req.method && req.path) summaryParts.push(req.method + " " + req.path);
      if (req.target_label) summaryParts.push(String(req.target_label));
      if (req.fill_value) summaryParts.push(String(req.fill_value));
    }
    if (summaryParts.length) {
      var sum = doc.createElement("p");
      sum.className = "daemon-confirm-summary";
      sum.textContent = summaryParts.join(" · ");
      copy.appendChild(sum);
    }

    var errorEl = doc.createElement("p");
    errorEl.className = "daemon-confirm-error";
    errorEl.textContent = wsT("js.dc.confirm.failed");
    errorEl.hidden = true;
    errorEl.setAttribute("role", "alert");
    copy.appendChild(errorEl);
    dcBanner.appendChild(copy);

    var controls = doc.createElement("div");
    controls.className = "daemon-confirm-controls";
    var settled = false;

    function makeAction(key, decision, cls) {
      var btn = doc.createElement("button");
      btn.type = "button";
      btn.className = "btn" + (cls ? " " + cls : "");
      btn.setAttribute("data-daemon-confirm-action", decision);
      btn.textContent = wsT(key);
      btn.addEventListener("click", function (e) {
        if (!e || e.isTrusted !== true) return;
        if (settled || dcActive !== req) return;
        settled = true;
        errorEl.hidden = true;
        var buttons = controls.querySelectorAll("button");
        buttons.forEach(function (b) { b.disabled = true; });
        fetch("/api/daemon/bridge-reply", {
          method: "POST",
          credentials: "same-origin",
          cache: "no-store",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            request_id: req.request_id,
            nonce: req.nonce,
            tab_id: req.tab_id || daemonTabId,
            decision: decision
          })
        }).then(function (res) {
          if (res.ok || res.status === 409) {
            if (dcActive === req) advanceDaemonConfirm();
            return;
          }
          throw new Error("HTTP " + res.status);
        }).catch(function () {
          if (dcActive !== req) return;
          settled = false;
          errorEl.hidden = false;
          buttons.forEach(function (b) { b.disabled = false; });
        });
      });
      return btn;
    }

    var approveBtn = makeAction("js.dc.confirm.approve", "approve", "pri");
    var denyBtn = makeAction("js.dc.confirm.deny", "deny", "");
    var dismissBtn = makeAction("js.dc.confirm.dismiss", "dismiss", "daemon-confirm-close");
    dismissBtn.title = dismissBtn.textContent;
    dismissBtn.setAttribute("aria-label", dismissBtn.textContent);
    dismissBtn.textContent = "×";
    controls.append(approveBtn, denyBtn, dismissBtn);
    dcBanner.appendChild(controls);
    setDaemonConfirmHidden(false);
  }

  function advanceDaemonConfirm() {
    var next = dcQueue.length ? dcQueue.shift() : null;
    renderDaemonConfirm(next);
  }

  function showDaemonConfirm(event) {
    if (!dcBanner || !event || typeof event.request_id !== "string" || !event.request_id || typeof event.nonce !== "string" || !event.nonce) return;
    if (dcActive && dcActive.request_id === event.request_id) return;
    if (dcQueue.some(function (q) { return q.request_id === event.request_id; })) return;
    var req = {
      request_id: event.request_id,
      nonce: event.nonce,
      tab_id: typeof event.tab_id === "string" ? event.tab_id : "",
      tool: typeof event.tool === "string" ? event.tool : "",
      method: typeof event.method === "string" ? event.method : "",
      path: typeof event.path === "string" ? event.path : "",
      action: typeof event.action === "string" ? event.action : "",
      target_label: typeof event.target_label === "string" ? event.target_label : "",
      fill_value: typeof event.fill_value === "string" ? event.fill_value : "",
      summary: typeof event.summary === "string" ? event.summary : "",
      expires_in_ms: Number(event.expires_in_ms) || 120000,
      deadline: Date.now() + (Number(event.expires_in_ms) > 0 ? Number(event.expires_in_ms) : 120000)
    };
    if (!dcActive) renderDaemonConfirm(req);
    else dcQueue.push(req);
  }

  function clearDaemonConfirm(requestId) {
    if (!dcBanner) return;
    if (!requestId) {
      dcQueue = [];
      renderDaemonConfirm(null);
      return;
    }
    if (dcActive && dcActive.request_id === requestId) {
      advanceDaemonConfirm();
      return;
    }
    dcQueue = dcQueue.filter(function (q) { return q.request_id !== requestId; });
  }
