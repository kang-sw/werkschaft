  // --- multi-tab daemon DOM snapshot & allowlisted UI bridge -----------------
  // Handles daemon-bridge-request WS push events in the local top-level shell
  // and relays framed session / settings operations over WsShell origin-checked
  // postMessage (KINDS: daemon-bridge-req / daemon-bridge-res). Zero arbitrary
  // JS eval and zero new SSE/WS connections.
  var DC_SENSITIVE_RE = /(?:password|passwd|secret|token|api[-_\s]?key|credential|cookie|bearer|private[-_\s]?key|auth[-_\s]?key)/i;
  var DC_REDACT_QUERY_RE = /([?&](?:token|login)=)[^&#\s"'<>]+/g;
  var DC_REDACT_BEARER_RE = /\bBearer\s+[A-Za-z0-9._~+/=-]{8,}/gi;
  var DC_REDACT_COOKIE_RE = /(ws_dashboard_(?:localhost|token|screenshot_render)=)[^;\s"'<>]+/g;
  var DC_REDACT_APIKEY_RE = /\b(?:sk-[A-Za-z0-9_-]{16,}|ghp_[A-Za-z0-9]{20,}|AIza[A-Za-z0-9_-]{20,})\b/g;
  var DC_SKIP_TAGS = { SCRIPT: 1, STYLE: 1, NOSCRIPT: 1, TEMPLATE: 1, SVG: 1, IFRAME: 1, LINK: 1, META: 1 };
  var DC_INTERACTIVE_ROLES = {
    button: 1, link: 1, tab: 1, "switch": 1, checkbox: 1, radio: 1,
    menuitem: 1, option: 1, textbox: 1, combobox: 1, searchbox: 1, slider: 1, spinbutton: 1
  };
  var DC_LANDMARK_TAGS = { NAV: 1, MAIN: 1, HEADER: 1, ASIDE: 1, DIALOG: 1, FORM: 1, H1: 1, H2: 1, H3: 1, H4: 1, H5: 1, H6: 1 };

  function dcRedactText(s) {
    if (!s) return "";
    return String(s)
      .replace(DC_REDACT_QUERY_RE, "$1[REDACTED]")
      .replace(DC_REDACT_BEARER_RE, "Bearer [REDACTED]")
      .replace(DC_REDACT_COOKIE_RE, "$1[REDACTED]")
      .replace(DC_REDACT_APIKEY_RE, "[REDACTED]");
  }

  function dcCompactText(s, maxLen) {
    var clean = String(s || "").replace(/\s+/g, " ").trim();
    if (maxLen && clean.length > maxLen) clean = clean.slice(0, maxLen);
    return dcRedactText(clean);
  }

  function dcIsExcluded(el) {
    if (!el || el.nodeType !== 1) return false;
    if (el.closest && el.closest('[data-ws-dom-exclude="1"], [data-daemon-confirm]')) return true;
    return false;
  }

  function dcIsVisible(el) {
    if (!el || el.nodeType !== 1) return false;
    if (el.hidden) return false;
    var tag = el.tagName;
    if (tag === "DIALOG" && !el.open) return false;
    var win = el.ownerDocument && el.ownerDocument.defaultView;
    if (win && typeof win.getComputedStyle === "function") {
      var cs = win.getComputedStyle(el);
      if (cs && (cs.display === "none" || cs.visibility === "hidden")) return false;
    }
    if (tag !== "OPTION" && typeof el.getClientRects === "function" && el.getClientRects().length === 0) {
      return false;
    }
    return true;
  }

  function dcIsSensitiveInput(el) {
    if (!el || el.nodeType !== 1) return false;
    var tag = el.tagName;
    if (tag !== "INPUT" && tag !== "TEXTAREA") return false;
    var inputType = tag === "INPUT" ? String(el.getAttribute("type") || el.type || "text").toLowerCase() : "textarea";
    if (inputType === "password") return true;
    var attrs = [
      el.getAttribute("name"),
      el.getAttribute("id"),
      el.getAttribute("autocomplete"),
      el.getAttribute("placeholder"),
      el.getAttribute("aria-label"),
      el.getAttribute("title")
    ];
    for (var i = 0; i < attrs.length; i++) {
      if (attrs[i] && DC_SENSITIVE_RE.test(attrs[i])) return true;
    }
    if (el.attributes) {
      for (var j = 0; j < el.attributes.length; j++) {
        var attr = el.attributes[j];
        if (attr && attr.name && attr.name.indexOf("data-ws-") === 0) {
          if (DC_SENSITIVE_RE.test(attr.name) || DC_SENSITIVE_RE.test(attr.value || "")) return true;
        }
      }
    }
    return false;
  }

  function dcSemanticRole(el) {
    var explicit = (el.getAttribute("role") || "").trim().toLowerCase();
    if (explicit) return explicit;
    var tag = el.tagName;
    if (tag === "A" && el.hasAttribute("href")) return "link";
    if (tag === "BUTTON") return "button";
    if (tag === "SUMMARY") return "button";
    if (tag === "TEXTAREA") return "textbox";
    if (tag === "SELECT") return "combobox";
    if (tag === "NAV") return "navigation";
    if (tag === "MAIN") return "main";
    if (tag === "HEADER") return "banner";
    if (tag === "ASIDE") return "complementary";
    if (tag === "DIALOG") return "dialog";
    if (tag === "FORM") return "form";
    if (/^H[1-6]$/.test(tag)) return "heading";
    if (tag === "INPUT") {
      var t = String(el.getAttribute("type") || el.type || "text").toLowerCase();
      if (t === "checkbox") return "checkbox";
      if (t === "radio") return "radio";
      if (t === "button" || t === "submit" || t === "reset") return "button";
      if (t === "search") return "searchbox";
      if (t === "range") return "slider";
      return "textbox";
    }
    if (el.isContentEditable) return "textbox";
    return "";
  }

  function dcIsInteractive(el, role) {
    var tag = el.tagName;
    if (tag === "BUTTON" || tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || tag === "SUMMARY") return true;
    if (tag === "A" && el.hasAttribute("href")) return true;
    if (el.isContentEditable) return true;
    if (role && DC_INTERACTIVE_ROLES[role]) return true;
    var ti = el.getAttribute("tabindex");
    if (ti !== null && ti !== "" && Number(ti) >= 0) return true;
    return false;
  }

  function dcAccessibleName(el, targetDoc) {
    var aria = el.getAttribute("aria-label");
    if (aria && aria.trim()) return dcCompactText(aria, 160);
    var lb = el.getAttribute("aria-labelledby");
    if (lb && targetDoc) {
      var parts = lb.trim().split(/\s+/).map(function (id) {
        var n = targetDoc.getElementById(id);
        return n ? n.textContent : "";
      }).filter(Boolean);
      if (parts.length) return dcCompactText(parts.join(" "), 160);
    }
    var tag = el.tagName;
    if ((tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") && targetDoc) {
      var id = el.getAttribute("id");
      if (id) {
        var labels = targetDoc.querySelectorAll("label[for]");
        for (var i = 0; i < labels.length; i++) {
          if (labels[i].getAttribute("for") === id) {
            var lt = dcCompactText(labels[i].textContent, 160);
            if (lt) return lt;
          }
        }
      }
      var wrap = el.closest && el.closest("label");
      if (wrap) {
        var wt = dcCompactText(wrap.textContent, 160);
        if (wt) return wt;
      }
      var ph = el.getAttribute("placeholder");
      if (ph && ph.trim()) return dcCompactText(ph, 160);
    }
    var title = el.getAttribute("title");
    if (title && title.trim()) return dcCompactText(title, 160);
    if (tag === "IMG") {
      var alt = el.getAttribute("alt");
      if (alt && alt.trim()) return dcCompactText(alt, 160);
    }
    if (tag === "INPUT") {
      var t = String(el.getAttribute("type") || el.type || "").toLowerCase();
      if ((t === "button" || t === "submit" || t === "reset") && el.value) {
        return dcCompactText(el.value, 160);
      }
      var nm = el.getAttribute("name");
      if (nm && nm.trim()) return dcCompactText(nm, 160);
    }
    return dcCompactText(el.textContent, 160);
  }

  function dcDirectText(el) {
    var buf = "";
    for (var child = el.firstChild; child; child = child.nextSibling) {
      if (child.nodeType === 3 && child.nodeValue) buf += child.nodeValue;
    }
    return dcCompactText(buf, 200);
  }

  function dcCollectDataWS(el) {
    if (!el.attributes) return null;
    var out = null;
    for (var i = 0; i < el.attributes.length; i++) {
      var a = el.attributes[i];
      if (!a || !a.name) continue;
      if (a.name.indexOf("data-ws-") === 0 || a.name === "data-tab" || a.name === "data-ctab" || a.name === "data-panel" || a.name === "data-entry" || a.name === "data-rid" || a.name === "data-scope-entry" || a.name === "data-dashboard-control-toggle" || a.name === "data-dashboard-control-switch") {
        if (!out) out = {};
        out[a.name] = dcRedactText(String(a.value || "").slice(0, 200));
      }
    }
    return out;
  }

  function dcSnapshotDocument(targetDoc, scopeName, opts, refStore) {
    var nodes = [];
    var truncated = false;
    var generation = Number(opts && opts.generation) || 1;
    var interactiveOnly = !!(opts && opts.interactive_only);
    var maxNodes = Math.min(1500, Math.max(1, Number(opts && opts.max_nodes) || 1500));
    var maxBytes = Math.min(65536, Math.max(1024, Number(opts && opts.max_bytes) || 65536));
    var refSeq = Number(opts && opts.ref_start) || 0;
    var approxBytes = 0;
    var skipIds = opts && opts.skip_ids ? opts.skip_ids : null;

    function walk(el, depth) {
      if (!el || el.nodeType !== 1) return;
      if (nodes.length >= maxNodes || approxBytes >= maxBytes) {
        truncated = true;
        return;
      }
      var tag = el.tagName;
      if (DC_SKIP_TAGS[tag]) return;
      if (skipIds && el.id && skipIds[el.id]) return;
      if (el.getAttribute("data-ws-dom-exclude") === "1" || el.hasAttribute("data-daemon-confirm")) return;
      if (!dcIsVisible(el)) return;

      var role = dcSemanticRole(el);
      var interactive = dcIsInteractive(el, role);
      var dataWS = dcCollectDataWS(el);
      var isLandmark = !!(DC_LANDMARK_TAGS[tag] || role === "alert" || role === "status" || role === "dialog" || role === "tablist" || role === "region");
      var directText = (!interactive && !isLandmark) ? dcDirectText(el) : "";

      var include = interactive || isLandmark || !!dataWS || (!interactiveOnly && directText.length > 0);
      if (interactiveOnly && !interactive && !isLandmark) include = false;

      if (include) {
        var entry = {
          scope: scopeName,
          tag: tag.toLowerCase(),
          depth: depth
        };
        if (role) entry.role = role;
        if (interactive) {
          refSeq += 1;
          var ref = "g" + generation + "-r" + refSeq;
          entry.ref = ref;
          if (refStore) refStore.set(ref, el);
        }
        var name = interactive || isLandmark ? dcAccessibleName(el, targetDoc) : "";
        if (name) entry.name = name;
        if (directText && directText !== name) entry.text = directText;
        if (dataWS) entry.data_ws = dataWS;

        if (tag === "A" && el.hasAttribute("href")) {
          entry.href = dcRedactText(el.getAttribute("href") || "");
        }
        if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") {
          if (tag === "INPUT") {
            entry.input_type = String(el.getAttribute("type") || el.type || "text").toLowerCase();
          }
          if (dcIsSensitiveInput(el)) {
            entry.masked = true;
            if (el.value) entry.value = "[REDACTED]";
          } else if (tag === "INPUT" && (entry.input_type === "checkbox" || entry.input_type === "radio")) {
            entry.checked = !!el.checked;
          } else if (el.value !== undefined && el.value !== "") {
            entry.value = dcCompactText(el.value, 240);
          }
        } else if (el.isContentEditable) {
          var ceVal = dcCompactText(el.textContent, 240);
          if (ceVal) entry.value = ceVal;
        }
        if (el.disabled || el.getAttribute("aria-disabled") === "true") entry.disabled = true;
        var ariaChecked = el.getAttribute("aria-checked");
        if (ariaChecked === "true" || ariaChecked === "false") entry.checked = ariaChecked === "true";
        var ariaPressed = el.getAttribute("aria-pressed");
        if (ariaPressed === "true" || ariaPressed === "false" || ariaPressed === "mixed") entry.pressed = ariaPressed;
        var ariaExpanded = el.getAttribute("aria-expanded");
        if (ariaExpanded === "true" || ariaExpanded === "false") entry.expanded = ariaExpanded === "true";
        var ariaSelected = el.getAttribute("aria-selected");
        if (ariaSelected === "true" || ariaSelected === "false") entry.selected = ariaSelected === "true";

        var est = JSON.stringify(entry).length + 2;
        if (approxBytes + est > maxBytes) {
          truncated = true;
          return;
        }
        approxBytes += est;
        nodes.push(entry);
      }

      if (nodes.length >= maxNodes || approxBytes >= maxBytes) {
        truncated = true;
        return;
      }
      var nextDepth = include ? depth + 1 : depth;
      for (var child = el.firstElementChild; child; child = child.nextElementSibling) {
        walk(child, nextDepth);
        if (truncated) return;
      }
    }

    if (targetDoc && targetDoc.body) walk(targetDoc.body, 0);
    return { nodes: nodes, truncated: truncated, ref_end: refSeq };
  }

  function dcClickElement(el) {
    if (!el || !el.isConnected) return { ok: false, error: "stale_ref" };
    if (dcIsExcluded(el)) return { ok: false, error: "denied" };
    if (el.disabled || el.getAttribute("aria-disabled") === "true") return { ok: false, error: "denied" };
    try {
      if (typeof el.scrollIntoView === "function") el.scrollIntoView({ block: "nearest", inline: "nearest" });
      if (typeof el.focus === "function") el.focus({ preventScroll: true });
      el.click();
      return { ok: true };
    } catch (_) {
      return { ok: false, error: "denied" };
    }
  }

  function dcFillElement(el, value) {
    if (!el || !el.isConnected) return { ok: false, error: "stale_ref" };
    if (dcIsExcluded(el)) return { ok: false, error: "denied" };
    if (el.disabled || el.readOnly || el.getAttribute("aria-disabled") === "true") return { ok: false, error: "denied" };
    var tag = el.tagName;
    var val = String(value === undefined || value === null ? "" : value);
    var masked = dcIsSensitiveInput(el);
    try {
      if (typeof el.scrollIntoView === "function") el.scrollIntoView({ block: "nearest", inline: "nearest" });
      if (typeof el.focus === "function") el.focus({ preventScroll: true });
      if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") {
        var win = el.ownerDocument && el.ownerDocument.defaultView;
        var proto = tag === "INPUT" ? win.HTMLInputElement && win.HTMLInputElement.prototype
          : tag === "TEXTAREA" ? win.HTMLTextAreaElement && win.HTMLTextAreaElement.prototype
          : win.HTMLSelectElement && win.HTMLSelectElement.prototype;
        var desc = proto && Object.getOwnPropertyDescriptor(proto, "value");
        if (desc && typeof desc.set === "function") desc.set.call(el, val);
        else el.value = val;
        el.dispatchEvent(new Event("input", { bubbles: true }));
        el.dispatchEvent(new Event("change", { bubbles: true }));
        return { ok: true, masked: masked };
      }
      if (el.isContentEditable) {
        el.textContent = val;
        el.dispatchEvent(new Event("input", { bubbles: true }));
        return { ok: true, masked: masked };
      }
      return { ok: false, error: "invalid_request" };
    } catch (_) {
      return { ok: false, error: "denied" };
    }
  }

  // Frame-side state (used when this script executes inside a framed session page).
  var dcFrameGen = 0;
  var dcFrameRefs = new Map();

  function dcClearFrameRefs() {
    dcFrameGen = 0;
    dcFrameRefs.clear();
  }
  if (framed) {
    window.addEventListener("hashchange", dcClearFrameRefs);
    window.addEventListener("popstate", dcClearFrameRefs);
    window.addEventListener("beforeunload", dcClearFrameRefs);
  }

  function dcSelectPanelInFrame(panel) {
    if (panel === "convo") {
      if (typeof setTab === "function" && grid) setTab("convo");
      if (typeof setCtab === "function" && grid) setCtab("convo");
      return true;
    }
    if (panel === "board") {
      if (typeof setTab === "function" && grid) { setTab("board"); return true; }
      return false;
    }
    if (panel === "files") {
      if (doc.querySelector('#ctabs [data-ctab="files"]') && typeof setCtab === "function" && grid) {
        setCtab("files");
        return true;
      }
      return typeof runGlobalShortcut === "function" ? runGlobalShortcut("files") : false;
    }
    if (panel === "term") {
      if (typeof setCtab === "function" && grid) { setCtab("term"); return true; }
      return false;
    }
    if (panel === "git") return typeof runGlobalShortcut === "function" ? runGlobalShortcut("git") : false;
    if (panel === "overview") {
      if (window.WsOverview && typeof window.WsOverview.active === "function" && window.WsOverview.active()) return true;
      return typeof runGlobalShortcut === "function" ? runGlobalShortcut("overview") : false;
    }
    if (panel === "ticketmap") {
      if (typeof ticketMap !== "undefined" && ticketMap && typeof ticketMap.isOpen === "function" && ticketMap.isOpen()) return true;
      return typeof runGlobalShortcut === "function" ? runGlobalShortcut("ticketmap") : false;
    }
    if (panel === "search") {
      if (typeof tkSearch === "function") { tkSearch(); return true; }
      return false;
    }
    return false;
  }

  function handleDaemonFrameBridgeReq(msg) {
    if (!framed || !msg || typeof shPost !== "function") return;
    var id = msg.id, op = msg.op, params = msg.params || {};
    function reply(ok, payload, err) {
      shPost("daemon-bridge-res", { id: id, ok: !!ok, payload: payload || null, error: err || null });
    }
    if (op === "dom.snapshot") {
      dcFrameGen = Number(params.generation) || 1;
      dcFrameRefs.clear();
      var res = dcSnapshotDocument(doc, "frame", params, dcFrameRefs);
      reply(true, {
        nodes: res.nodes,
        truncated: res.truncated,
        ref_end: res.ref_end,
        path: String(location.pathname || "/"),
        hash: String(location.hash || ""),
        reply_id: String(rid || ""),
        title: String(doc.title || "")
      }, null);
      return;
    }
    if (op === "ui.click_ref") {
      var gen = Number(params.generation) || 0;
      if (!dcFrameGen || (gen && gen !== dcFrameGen)) { reply(false, null, "stale_ref"); return; }
      var el = dcFrameRefs.get(String(params.ref || ""));
      if (!el) { reply(false, null, "ref_not_found"); return; }
      var clicked = dcClickElement(el);
      if (!clicked.ok) { reply(false, null, clicked.error); return; }
      reply(true, { route: String(location.pathname || "/"), hash: String(location.hash || ""), reply_id: String(rid || "") }, null);
      return;
    }
    if (op === "ui.fill_ref") {
      var fgen = Number(params.generation) || 0;
      if (!dcFrameGen || (fgen && fgen !== dcFrameGen)) { reply(false, null, "stale_ref"); return; }
      var fel = dcFrameRefs.get(String(params.ref || ""));
      if (!fel) { reply(false, null, "ref_not_found"); return; }
      var filled = dcFillElement(fel, params.value);
      if (!filled.ok) { reply(false, null, filled.error); return; }
      reply(true, { route: String(location.pathname || "/"), hash: String(location.hash || ""), reply_id: String(rid || ""), masked: !!filled.masked }, null);
      return;
    }
    if (op === "ui.select_panel") {
      var ok = dcSelectPanelInFrame(String(params.panel || ""));
      reply(ok, ok ? { panel: String(params.panel || ""), route: String(location.pathname || "/"), reply_id: String(rid || "") } : null, ok ? null : "invalid_request");
      return;
    }
    if (op === "ui.reveal_entry") {
      var entryId = String(params.entry_id || "");
      if (entryId) {
        var wantHash = "#entry=" + encodeURIComponent(entryId);
        if (location.hash !== wantHash) {
          try { location.hash = wantHash; } catch (_) {}
        }
        var entries = doc.querySelectorAll("[data-entry]");
        for (var i = 0; i < entries.length; i++) {
          if (entries[i].getAttribute("data-entry") === entryId) {
            if (typeof entries[i].scrollIntoView === "function") entries[i].scrollIntoView({ block: "center", inline: "nearest" });
            break;
          }
        }
      }
      reply(true, { route: String(location.pathname || "/"), hash: String(location.hash || ""), reply_id: String(rid || "") }, null);
      return;
    }
    reply(false, null, "invalid_request");
  }

  // Shell-side bridge coordinator (top-level local shell only).
  var dcShellGen = 0;
  var dcShellRefs = new Map();
  var dcRemoteRefTargets = new Map();
  var dcFramePending = new Map();
  var dcFrameCallSeq = 0;

  function dcInvalidateShellRefs() {
    dcShellGen = 0;
    dcShellRefs.clear();
    dcRemoteRefTargets.clear();
  }

  function dcCallFrame(entry, op, params) {
    return new Promise(function (resolve) {
      if (!entry || !entry.el || !entry.el.contentWindow) {
        resolve({ ok: false, payload: null, error: "tab_gone" });
        return;
      }
      var id = "dbr-" + (++dcFrameCallSeq);
      var timer = setTimeout(function () {
        if (!dcFramePending.has(id)) return;
        dcFramePending.delete(id);
        resolve({ ok: false, payload: null, error: "timeout" });
      }, 5000);
      dcFramePending.set(id, {
        win: entry.el.contentWindow,
        resolve: function (res) {
          clearTimeout(timer);
          resolve(res);
        }
      });
      try {
        entry.el.contentWindow.postMessage({
          wsShell: "daemon-bridge-req",
          id: id,
          op: op,
          params: params || {}
        }, location.origin);
      } catch (_) {
        clearTimeout(timer);
        dcFramePending.delete(id);
        resolve({ ok: false, payload: null, error: "tab_gone" });
      }
    });
  }

  function handleDaemonBridgeRes(sourceWin, msg) {
    if (!shell || !msg || typeof msg.id !== "string") return;
    var pending = dcFramePending.get(msg.id);
    if (!pending || pending.win !== sourceWin) return;
    dcFramePending.delete(msg.id);
    pending.resolve({
      ok: !!msg.ok,
      payload: msg.payload || null,
      error: msg.error || null
    });
  }

  var DC_SCREENSHOT_MAX_BYTES = 8 * 1024 * 1024;

  function dcBase64ToBytes(b64) {
    if (typeof b64 !== "string" || !b64) return null;
    try {
      var bin = atob(b64);
      var out = new Uint8Array(bin.length);
      for (var i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
      return out;
    } catch (_) {
      return null;
    }
  }

  function postDaemonBridgeReply(req, payload, errCode) {
    var body = {
      request_id: req.request_id,
      nonce: req.nonce,
      tab_id: req.tab_id || daemonTabId
    };
    if (errCode) body.error = String(errCode);
    else body.payload = payload || {};
    return fetch("/api/daemon/bridge-reply", {
      method: "POST",
      credentials: "same-origin",
      cache: "no-store",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body)
    }).catch(function () {});
  }

  function postDaemonBridgeBinaryReply(req, bytes, contentType) {
    return fetch("/api/daemon/bridge-reply", {
      method: "POST",
      credentials: "same-origin",
      cache: "no-store",
      headers: {
        "Content-Type": contentType || "image/png",
        "X-WS-Request-ID": String(req.request_id || ""),
        "X-WS-Nonce": String(req.nonce || ""),
        "X-WS-Tab-ID": String(req.tab_id || daemonTabId || "")
      },
      body: bytes
    }).catch(function () {});
  }

  async function handleDaemonBridgeRequest(req) {
    if (!daemonBridgeEnabled || !req || typeof req.request_id !== "string" || typeof req.nonce !== "string") return;
    var op = String(req.op || "");
    var params = req.params && typeof req.params === "object" ? req.params : {};

    if (op === "dom.snapshot") {
      var generation = Number(params.generation) || 1;
      var scope = String(params.scope || "all");
      var maxNodes = Math.min(1500, Math.max(1, Number(params.max_nodes) || 1500));
      var maxBytes = Math.min(65536, Math.max(1024, Number(params.max_bytes) || 65536));
      var interactiveOnly = !!params.interactive_only;

      dcShellGen = generation;
      dcShellRefs.clear();
      dcRemoteRefTargets.clear();

      var allNodes = [];
      var truncated = false;
      var refSeq = 0;

      if (scope === "all" || scope === "shell") {
        var shellRes = dcSnapshotDocument(doc, "shell", {
          generation: generation,
          interactive_only: interactiveOnly,
          max_nodes: maxNodes,
          max_bytes: maxBytes,
          ref_start: refSeq,
          skip_ids: { frames: 1, "workflow-settings": 1 }
        }, dcShellRefs);
        allNodes = allNodes.concat(shellRes.nodes);
        truncated = truncated || shellRes.truncated;
        refSeq = shellRes.ref_end;
      }

      var frameInfo = null;
      if (shOn) {
        frameInfo = {
          path: shOn.f ? String(shOn.f.path || "") : "",
          hash: shOn.f ? String(shOn.f.hash || "") : "",
          reply_id: shOn.f ? String(shOn.f.rid || "") : "",
          title: String(shOn.title || ""),
          ready: !!(shOn.ready || shOn.loaded)
        };
      }
      if ((scope === "all" || scope === "frame") && shOn && allNodes.length < maxNodes) {
        var frameRes = await dcCallFrame(shOn, "dom.snapshot", {
          generation: generation,
          interactive_only: interactiveOnly,
          max_nodes: maxNodes - allNodes.length,
          max_bytes: maxBytes,
          ref_start: refSeq
        });
        if (frameRes.ok && frameRes.payload && Array.isArray(frameRes.payload.nodes)) {
          for (var i = 0; i < frameRes.payload.nodes.length; i++) {
            var fn = frameRes.payload.nodes[i];
            if (fn && fn.ref) dcRemoteRefTargets.set(fn.ref, "frame");
          }
          allNodes = allNodes.concat(frameRes.payload.nodes);
          truncated = truncated || !!frameRes.payload.truncated;
          refSeq = Number(frameRes.payload.ref_end) || refSeq;
          frameInfo = {
            path: String(frameRes.payload.path || (frameInfo ? frameInfo.path : "")),
            hash: String(frameRes.payload.hash || (frameInfo ? frameInfo.hash : "")),
            reply_id: String(frameRes.payload.reply_id || (frameInfo ? frameInfo.reply_id : "")),
            title: String(frameRes.payload.title || (frameInfo ? frameInfo.title : "")),
            ready: true
          };
        }
      }

      if ((scope === "all" || scope === "settings") && shSettingsEntry && allNodes.length < maxNodes) {
        var setRes = await dcCallFrame(shSettingsEntry, "dom.snapshot", {
          generation: generation,
          interactive_only: interactiveOnly,
          max_nodes: maxNodes - allNodes.length,
          max_bytes: maxBytes,
          ref_start: refSeq
        });
        if (setRes.ok && setRes.payload && Array.isArray(setRes.payload.nodes)) {
          for (var j = 0; j < setRes.payload.nodes.length; j++) {
            var sn = setRes.payload.nodes[j];
            if (sn && sn.ref) dcRemoteRefTargets.set(sn.ref, "settings");
          }
          allNodes = allNodes.concat(setRes.payload.nodes);
          truncated = truncated || !!setRes.payload.truncated;
          refSeq = Number(setRes.payload.ref_end) || refSeq;
        }
      }

      var openDialogs = [];
      doc.querySelectorAll("dialog[open]").forEach(function (dlg) {
        if (dcIsExcluded(dlg)) return;
        var label = dlg.getAttribute("aria-label") || dlg.id || "dialog";
        openDialogs.push(dcCompactText(label, 120));
      });

      await postDaemonBridgeReply(req, {
        tab_id: daemonTabId,
        generation: generation,
        route: String(location.pathname || "/"),
        hash: String(location.hash || ""),
        reply_id: String(rid || ""),
        title: String(doc.title || ""),
        frame: frameInfo,
        modals: {
          settings_open: !!shSettingsEntry,
          settings_href: shSettingsEntry ? String(shSettingsEntry.href || "") : "",
          rail_open: typeof railShown === "function" ? !!railShown() : root.classList.contains("rail-open"),
          dialogs: openDialogs
        },
        nodes: allNodes,
        node_count: allNodes.length,
        truncated: truncated
      }, null);
      return;
    }

    if (op === "ui.navigate") {
      var navPath = String(params.path || "");
      if (!navPath || navPath.charAt(0) !== "/") {
        await postDaemonBridgeReply(req, null, "invalid_request");
        return;
      }
      dcInvalidateShellRefs();
      if (navPath.indexOf("/settings") === 0 && typeof shOpenSettings === "function") {
        shOpenSettings(new URL(navPath, location.origin));
      } else {
        if (typeof shCloseSettings === "function" && shSettingsEntry) shCloseSettings();
        if (typeof shOpen === "function") shOpen(navPath, true);
      }
      if (typeof sendDaemonHeartbeat === "function") sendDaemonHeartbeat();
      await postDaemonBridgeReply(req, {
        ok: true,
        action: "navigate",
        route: String(location.pathname || "/"),
        hash: String(location.hash || ""),
        reply_id: String(rid || "")
      }, null);
      return;
    }

    if (op === "ui.focus_session") {
      var replyId = String(params.reply_id || "");
      var entryId = String(params.entry_id || "");
      if (!replyId) {
        await postDaemonBridgeReply(req, null, "invalid_request");
        return;
      }
      dcInvalidateShellRefs();
      if (typeof shCloseSettings === "function" && shSettingsEntry) shCloseSettings();
      var targetHref = "/s/" + encodeURIComponent(replyId) + (entryId ? "#entry=" + encodeURIComponent(entryId) : "");
      var alreadyActive = !!(shOn && shOn.f && shOn.f.rid === replyId);
      if (typeof shOpen === "function") shOpen(targetHref, true);
      if (alreadyActive && entryId && shOn) {
        await dcCallFrame(shOn, "ui.reveal_entry", { entry_id: entryId });
      }
      if (typeof sendDaemonHeartbeat === "function") sendDaemonHeartbeat();
      await postDaemonBridgeReply(req, {
        ok: true,
        action: "focus_session",
        reply_id: replyId,
        route: String(location.pathname || "/"),
        hash: String(location.hash || "")
      }, null);
      return;
    }

    if (op === "ui.open_settings") {
      if (typeof shOpenSettings !== "function") {
        await postDaemonBridgeReply(req, null, "denied");
        return;
      }
      var sq = new URLSearchParams();
      if (params.scope) sq.set("scope", String(params.scope));
      if (params.target) sq.set("target", String(params.target));
      if (params.worktree) sq.set("worktree", String(params.worktree));
      if (params.session) sq.set("session", String(params.session));
      if (params.section) sq.set("section", String(params.section));
      var shref = "/settings" + (sq.toString() ? "?" + sq.toString() : "");
      shOpenSettings(new URL(shref, location.origin));
      await postDaemonBridgeReply(req, {
        ok: true,
        action: "open_settings",
        route: shref
      }, null);
      return;
    }

    if (op === "ui.select_panel") {
      var panel = String(params.panel || "");
      if (panel === "rail") {
        if (typeof railOpen === "function") railOpen(true);
        await postDaemonBridgeReply(req, { ok: true, action: "select_panel", panel: "rail", route: String(location.pathname || "/"), reply_id: String(rid || "") }, null);
        return;
      }
      if (panel === "search") {
        if (typeof shSearch === "function") shSearch();
        await postDaemonBridgeReply(req, { ok: true, action: "select_panel", panel: "search", route: String(location.pathname || "/"), reply_id: String(rid || "") }, null);
        return;
      }
      if (!shOn) {
        await postDaemonBridgeReply(req, null, "no_active_tab");
        return;
      }
      var panelRes = await dcCallFrame(shOn, "ui.select_panel", { panel: panel });
      if (!panelRes.ok) {
        await postDaemonBridgeReply(req, null, panelRes.error || "invalid_request");
        return;
      }
      await postDaemonBridgeReply(req, Object.assign({ ok: true, action: "select_panel", panel: panel }, panelRes.payload || {}), null);
      return;
    }

    if (op === "ui.click_ref" || op === "ui.fill_ref") {
      var ref = String(params.ref || "");
      var reqGen = Number(params.generation) || 0;
      if (!dcShellGen || (reqGen && reqGen !== dcShellGen)) {
        await postDaemonBridgeReply(req, null, "stale_ref");
        return;
      }
      if (dcShellRefs.has(ref)) {
        var localEl = dcShellRefs.get(ref);
        var localOut = op === "ui.click_ref" ? dcClickElement(localEl) : dcFillElement(localEl, params.value);
        if (!localOut.ok) {
          await postDaemonBridgeReply(req, null, localOut.error);
          return;
        }
        await postDaemonBridgeReply(req, {
          ok: true,
          action: op === "ui.click_ref" ? "click_ref" : "fill_ref",
          ref: ref,
          route: String(location.pathname || "/"),
          hash: String(location.hash || ""),
          reply_id: String(rid || ""),
          masked: !!localOut.masked
        }, null);
        return;
      }
      var targetKind = dcRemoteRefTargets.get(ref);
      var targetEntry = targetKind === "frame" ? shOn : targetKind === "settings" ? shSettingsEntry : null;
      if (!targetKind) {
        await postDaemonBridgeReply(req, null, "ref_not_found");
        return;
      }
      if (!targetEntry) {
        await postDaemonBridgeReply(req, null, "stale_ref");
        return;
      }
      var remoteOut = await dcCallFrame(targetEntry, op, params);
      if (!remoteOut.ok) {
        await postDaemonBridgeReply(req, null, remoteOut.error || "stale_ref");
        return;
      }
      await postDaemonBridgeReply(req, Object.assign({
        ok: true,
        action: op === "ui.click_ref" ? "click_ref" : "fill_ref",
        ref: ref
      }, remoteOut.payload || {}), null);
      return;
    }

    if (op === "screenshot.capture") {
      if (!window.wsDaemonNativeCapture || typeof window.wsDaemonNativeCapture.capture !== "function") {
        await postDaemonBridgeReply(req, null, "screenshot_unavailable");
        return;
      }
      var capRes = null;
      try {
        capRes = await window.wsDaemonNativeCapture.capture();
      } catch (_) {
        capRes = null;
      }
      if (!capRes || !capRes.ok || typeof capRes.png_base64 !== "string") {
        await postDaemonBridgeReply(req, null, (capRes && capRes.error) ? String(capRes.error) : "screenshot_unavailable");
        return;
      }
      var pngBytes = dcBase64ToBytes(capRes.png_base64);
      if (!pngBytes || pngBytes.byteLength === 0) {
        await postDaemonBridgeReply(req, null, "screenshot_unavailable");
        return;
      }
      if (pngBytes.byteLength > DC_SCREENSHOT_MAX_BYTES) {
        await postDaemonBridgeReply(req, null, "payload_too_large");
        return;
      }
      await postDaemonBridgeBinaryReply(req, pngBytes, "image/png");
      return;
    }

    // Interactive guide overlay lives in dashboard-daemon-guide.js.
    if (op === "guide.highlight" && window.WsDaemonGuide && typeof window.WsDaemonGuide.highlight === "function") {
      var gres;
      try { gres = window.WsDaemonGuide.highlight(params) || {}; } catch (_) { gres = { error: "highlight_failed" }; }
      await postDaemonBridgeReply(req, gres.error ? null : gres.payload, gres.error || null);
      return;
    }

    await postDaemonBridgeReply(req, null, "invalid_request");
  }

  if (daemonBridgeEnabled) {
    doc.addEventListener("ws-daemon-bridge-request", function (e) {
      if (e && e.detail) handleDaemonBridgeRequest(e.detail);
    });
    doc.addEventListener("ws-shell-route", dcInvalidateShellRefs);
    window.addEventListener("popstate", dcInvalidateShellRefs);
  }
