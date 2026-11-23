var WsSettingsCombobox = (function () {
  "use strict";
  var FALLBACK_HARNESSES = ["claude", "codex", "agy", "werk"];
  var PENDING_RETRY_MS = 1500;
  var choicesPromise = null;
  var choicesData = null;
  var seq = 0;
  var activeClose = null;
  var waiters = [];
  // refreshHooks re-render the pickers that are open (or waiting to open) when a
  // retried choices answer lands; each is {gone, refresh}, and one whose picker
  // left the page is dropped.
  var refreshHooks = [];
  var MAX_PENDING_RETRIES = 20;
  var pendingRetries = 0;

  function tr(key) {
    return typeof window !== "undefined" && typeof window.wsT === "function" ? window.wsT(key) : key;
  }

  function loadChoices() {
    if (choicesData) return Promise.resolve(choicesData);
    if (choicesPromise) return choicesPromise;
    if (typeof fetch !== "function") {
      choicesData = { harnesses: [], failed: true };
      return Promise.resolve(choicesData);
    }
    choicesPromise = fetch("/support/choices", { credentials: "same-origin", cache: "no-store" })
      .then(function (resp) {
        if (!resp || !resp.ok) throw new Error("choices unavailable");
        return resp.json();
      })
      .then(function (json) {
        if (!json || !Array.isArray(json.harnesses)) throw new Error("invalid choices");
        // pickers are harnesses that list models for this picker but open no
        // support conversation or lead (a worker-only backend): beside the rest here.
        choicesData = { harnesses: json.harnesses.concat(Array.isArray(json.pickers) ? json.pickers : []), failed: false };
        // A picker's model list is fetched in the background and is missing from
        // this answer: ask again shortly rather than keep the answer for the page.
        if (json.pickers_pending && typeof setTimeout === "function" && pendingRetries < MAX_PENDING_RETRIES) {
          pendingRetries++;
          setTimeout(function () {
            choicesData = null;
            choicesPromise = null;
            // Fetched here, not left for the next interaction: an open picker
            // shows the list as soon as it arrives.
            loadChoices().then(function () {
              refreshHooks.slice().forEach(function (hook) {
                if (!hook.gone()) { try { hook.refresh(); } catch (_) {} }
              });
              pruneRefreshHooks();
            });
          }, PENDING_RETRY_MS);
        } else if (!json.pickers_pending) {
          pendingRetries = 0;
        }
        return choicesData;
      })
      .catch(function () {
        choicesData = { harnesses: [], failed: true };
        return choicesData;
      })
      .then(function (resolved) {
        var list = waiters.splice(0, waiters.length);
        list.forEach(function (fn) { try { fn(resolved); } catch (_) {} });
        return resolved;
      });
    return choicesPromise;
  }

  function pruneRefreshHooks() {
    refreshHooks = refreshHooks.filter(function (hook) { return !hook.gone(); });
  }

  function onChoicesLoaded(fn) {
    if (choicesData) return;
    if (waiters.indexOf(fn) < 0) waiters.push(fn);
  }

  function harnessEntry(name) {
    var key = String(name || "").trim().toLowerCase();
    if (!key || !choicesData || !Array.isArray(choicesData.harnesses)) return null;
    for (var i = 0; i < choicesData.harnesses.length; i++) {
      var h = choicesData.harnesses[i];
      if (h && String(h.harness || "").trim().toLowerCase() === key) return h;
    }
    return null;
  }

  function backendOptions() {
    if (choicesData && Array.isArray(choicesData.harnesses) && choicesData.harnesses.length) {
      return choicesData.harnesses
        .filter(function (h) { return h && h.harness; })
        .map(function (h) {
          return {
            id: String(h.harness),
            tag: h.available === false ? tr("js.settings.combo.unavailable") : "",
            unavailable: h.available === false
          };
        });
    }
    return FALLBACK_HARNESSES.map(function (id) { return { id: id }; });
  }

  function modelOptions(harnessName) {
    var h = harnessEntry(harnessName);
    if (!h || !Array.isArray(h.models)) return [];
    return h.models
      .filter(function (m) { return m && m.id && (!m.provider || m.provider === "openrouter"); })
      .map(function (m) { return { id: String(m.id), thinking: m.thinking === true }; });
  }

  function effortOptions(harnessName) {
    var h = harnessEntry(harnessName);
    if (!h || !Array.isArray(h.efforts)) return [];
    return h.efforts
      .filter(function (e) { return Boolean(e); })
      .map(function (e) { return { id: String(e) }; });
  }

  function fuzzyScore(id, qq) {
    if (!qq) return 0;
    var s = String(id).toLowerCase();
    var qs = String(qq).toLowerCase().replace(/\s+/g, "");
    if (!qs) return 0;
    var at = -1, score = 0, run = 0;
    for (var i = 0; i < qs.length; i++) {
      var j = s.indexOf(qs[i], at + 1);
      if (j < 0) return -1;
      run = j === at + 1 ? run + 1 : 0;
      score += 1 + run * 2 + (j === 0 || /[^a-z0-9]/.test(s[j - 1]) ? 3 : 0);
      at = j;
    }
    return score - s.length / 100;
  }

  function filterOptions(options, query) {
    var q = String(query || "").trim();
    if (!q) return options.slice();
    return options
      .map(function (opt, idx) { return { opt: opt, idx: idx, score: fuzzyScore(opt.id, q) }; })
      .filter(function (item) { return item.score >= 0; })
      .sort(function (a, b) { return b.score - a.score || a.idx - b.idx; })
      .map(function (item) { return item.opt; });
  }

  function attach(input, opts) {
    opts = opts || {};
    var doc = input.ownerDocument || document;
    var win = doc.defaultView || (typeof window !== "undefined" ? window : null);
    var translate = opts.tr || tr;
    var listId = "ws-scb-list-" + (++seq);
    var wrap = doc.createElement("div");
    wrap.className = "scb";
    if (input.parentNode) input.parentNode.insertBefore(wrap, input);
    input.classList.add("scb-inp");
    input.setAttribute("role", "combobox");
    input.setAttribute("aria-autocomplete", "list");
    input.setAttribute("aria-expanded", "false");
    input.setAttribute("aria-controls", listId);
    input.autocomplete = "off";
    input.spellcheck = false;

    var btn = doc.createElement("button");
    btn.type = "button";
    btn.className = "scb-btn";
    btn.tabIndex = -1;
    btn.disabled = !!(input.disabled || input.readOnly);
    btn.setAttribute("aria-label", translate("js.settings.combo.toggle"));
    btn.title = translate("js.settings.combo.toggle");
    btn.setAttribute("aria-expanded", "false");
    btn.setAttribute("aria-controls", listId);
    var chev = doc.createElement("span");
    chev.className = "scb-chev";
    chev.setAttribute("aria-hidden", "true");
    chev.textContent = "▾";
    btn.appendChild(chev);
    wrap.appendChild(input);
    wrap.appendChild(btn);

    var popup = doc.createElement("ul");
    popup.id = listId;
    popup.className = "scb-pop";
    popup.setAttribute("role", "listbox");
    var lbl = input.getAttribute("aria-label") || opts.label || "";
    if (lbl) popup.setAttribute("aria-label", lbl);
    popup.hidden = true;

    var isOpen = false;
    var showAll = false;
    var activeIndex = -1;
    var renderedItems = [];
    var suppressFocusOpen = false;
    var suppressInputOpen = false;
    var pendingOpen = null;
    var listenersAttached = false;
    var boundDialog = null;

    function isDisabled() {
      return !!(input.disabled || input.readOnly);
    }

    function syncAria() {
      var expanded = isOpen ? "true" : "false";
      input.setAttribute("aria-expanded", expanded);
      btn.setAttribute("aria-expanded", expanded);
      btn.disabled = isDisabled();
      if (isOpen && activeIndex >= 0 && renderedItems[activeIndex]) {
        input.setAttribute("aria-activedescendant", listId + "-opt-" + activeIndex);
      } else {
        input.removeAttribute("aria-activedescendant");
      }
    }

    function positionPopup() {
      if (!isOpen || !wrap.isConnected || isDisabled()) {
        if (isOpen) close();
        return;
      }
      var rect = wrap.getBoundingClientRect();
      var vw = (win && win.innerWidth) || doc.documentElement.clientWidth || 1024;
      var vh = (win && win.innerHeight) || doc.documentElement.clientHeight || 768;
      if ((rect.width === 0 && rect.height === 0) || rect.bottom < 0 || rect.top > vh) {
        close();
        return;
      }
      var width = Math.min(Math.max(rect.width, 176), Math.max(176, vw - 16));
      var left = Math.max(8, Math.min(rect.left, vw - width - 8));
      var gap = 4;
      var spaceBelow = vh - rect.bottom - gap - 8;
      var spaceAbove = rect.top - gap - 8;
      var maxH = 240;
      if (spaceBelow >= 130 || spaceBelow >= spaceAbove) {
        popup.style.top = Math.round(rect.bottom + gap) + "px";
        popup.style.bottom = "auto";
        popup.style.maxHeight = Math.max(80, Math.min(maxH, Math.floor(spaceBelow))) + "px";
      } else {
        popup.style.top = "auto";
        popup.style.bottom = Math.round(vh - rect.top + gap) + "px";
        popup.style.maxHeight = Math.max(80, Math.min(maxH, Math.floor(spaceAbove))) + "px";
      }
      popup.style.left = Math.round(left) + "px";
      popup.style.width = Math.round(width) + "px";
    }

    function onDocPointerDown(e) {
      if (!wrap.isConnected) { close(); return; }
      if (!wrap.contains(e.target) && !popup.contains(e.target)) close();
    }

    function onWinResize() {
      positionPopup();
    }

    function onWinScroll(e) {
      if (e.target === popup || (popup.contains && popup.contains(e.target))) return;
      positionPopup();
    }

    function onDialogClose() {
      close();
    }

    function attachListeners() {
      if (listenersAttached) return;
      listenersAttached = true;
      doc.addEventListener("pointerdown", onDocPointerDown, true);
      if (win) {
        win.addEventListener("resize", onWinResize);
        win.addEventListener("scroll", onWinScroll, true);
      }
      boundDialog = input.closest ? input.closest("dialog") : null;
      if (boundDialog) boundDialog.addEventListener("close", onDialogClose);
    }

    function detachListeners() {
      if (!listenersAttached) return;
      listenersAttached = false;
      doc.removeEventListener("pointerdown", onDocPointerDown, true);
      if (win) {
        win.removeEventListener("resize", onWinResize);
        win.removeEventListener("scroll", onWinScroll, true);
      }
      if (boundDialog) {
        boundDialog.removeEventListener("close", onDialogClose);
        boundDialog = null;
      }
    }

    function close() {
      pendingOpen = null;
      if (activeClose === close) activeClose = null;
      popup.hidden = true;
      if (popup.parentNode) popup.parentNode.removeChild(popup);
      isOpen = false;
      showAll = false;
      activeIndex = -1;
      renderedItems = [];
      syncAria();
      detachListeners();
    }

    function setActive(index) {
      activeIndex = index;
      var children = popup.children;
      for (var i = 0; i < children.length; i++) {
        children[i].classList.toggle("is-active", i === activeIndex);
      }
      syncAria();
      if (activeIndex >= 0 && children[activeIndex] && children[activeIndex].scrollIntoView) {
        children[activeIndex].scrollIntoView({ block: "nearest" });
      }
    }

    function renderList() {
      if (isDisabled() || !wrap.isConnected) {
        close();
        return;
      }
      var all = (opts.getOptions && opts.getOptions()) || [];
      var items = showAll ? all.slice() : filterOptions(all, input.value);
      renderedItems = items;
      if (!items.length) {
        var keepPending = pendingOpen;
        close();
        pendingOpen = keepPending;
        return;
      }
      if (activeIndex >= items.length) activeIndex = items.length - 1;
      if (activeClose && activeClose !== close) activeClose();
      activeClose = close;

      popup.replaceChildren();
      var selectedEl = null;
      items.forEach(function (opt, idx) {
        var li = doc.createElement("li");
        li.id = listId + "-opt-" + idx;
        li.className = "scb-opt" + (idx === activeIndex ? " is-active" : "") + (opt.unavailable ? " is-unavail" : "");
        li.setAttribute("role", "option");
        var isSel = opt.id === input.value;
        li.setAttribute("aria-selected", String(isSel));
        li.dataset.value = opt.id;
        var idSpan = doc.createElement("span");
        idSpan.className = "scb-opt-id";
        idSpan.textContent = opt.id;
        li.appendChild(idSpan);
        if (opt.thinking) {
          var tTag = doc.createElement("span");
          tTag.className = "scb-opt-tag";
          tTag.textContent = translate("js.settings.combo.thinking");
          li.appendChild(tTag);
        } else if (opt.tag) {
          var mTag = doc.createElement("span");
          mTag.className = "scb-opt-tag muted";
          mTag.textContent = opt.tag;
          li.appendChild(mTag);
        }
        if (isSel) selectedEl = li;
        popup.appendChild(li);
      });

      var mount = (input.closest && input.closest("dialog")) || doc.body;
      if (popup.parentNode !== mount) mount.appendChild(popup);
      popup.hidden = false;
      isOpen = true;
      syncAria();
      attachListeners();
      positionPopup();
      if (activeIndex >= 0 && popup.children[activeIndex] && popup.children[activeIndex].scrollIntoView) {
        popup.children[activeIndex].scrollIntoView({ block: "nearest" });
      } else if (showAll && selectedEl && selectedEl.scrollIntoView) {
        selectedEl.scrollIntoView({ block: "nearest" });
      }
    }

    function onChoicesArrived() {
      if (!wrap.isConnected || isDisabled()) return;
      if (isOpen || pendingOpen) {
        var wantAll = isOpen ? showAll : pendingOpen === "all";
        if (doc.activeElement === input || wantAll) {
          showAll = wantAll;
          renderList();
        }
      }
    }

    // A retried choices answer (a picker's model list that was still being
    // fetched) re-renders this picker if it is open, without a keystroke.
    var seen = false;
    pruneRefreshHooks();
    refreshHooks.push({
      // Not gone before it was ever attached: the page builds its pickers
      // before it inserts them.
      gone: function () {
        if (wrap.isConnected) seen = true;
        return seen && !wrap.isConnected;
      },
      refresh: onChoicesArrived
    });

    function open(unfiltered) {
      if (isDisabled()) return;
      showAll = !!unfiltered;
      activeIndex = -1;
      onChoicesLoaded(onChoicesArrived);
      loadChoices();
      renderList();
    }

    function selectOption(opt) {
      pendingOpen = null;
      suppressInputOpen = true;
      input.value = opt.id;
      close();
      input.dispatchEvent(new Event("input", { bubbles: true }));
      input.dispatchEvent(new Event("change", { bubbles: true }));
      suppressInputOpen = false;
    }

    input.addEventListener("focus", function () {
      if (suppressFocusOpen || isDisabled()) return;
      pendingOpen = "filter";
      open(false);
    });

    input.addEventListener("click", function () {
      if (!isOpen && !isDisabled()) {
        pendingOpen = "filter";
        open(false);
      }
    });

    input.addEventListener("input", function () {
      if (suppressInputOpen || isDisabled()) return;
      pendingOpen = "filter";
      open(false);
    });

    input.addEventListener("keydown", function (e) {
      if (e.isComposing || isDisabled()) return;
      if (e.key === "ArrowDown") {
        e.preventDefault();
        if (!isOpen) {
          pendingOpen = "filter";
          open(false);
          if (!isOpen) {
            pendingOpen = "all";
            open(true);
          }
          if (isOpen && renderedItems.length) setActive(0);
        } else if (renderedItems.length) {
          setActive(activeIndex < 0 ? 0 : (activeIndex + 1) % renderedItems.length);
        }
      } else if (e.key === "ArrowUp") {
        if (!isOpen) return;
        e.preventDefault();
        if (renderedItems.length) {
          setActive(activeIndex <= 0 ? renderedItems.length - 1 : activeIndex - 1);
        }
      } else if (e.key === "Enter") {
        if (!isOpen) return;
        if (activeIndex >= 0 && renderedItems[activeIndex]) {
          e.preventDefault();
          selectOption(renderedItems[activeIndex]);
        } else {
          close();
        }
      } else if (e.key === "Escape") {
        pendingOpen = null;
        if (isOpen) {
          e.preventDefault();
          e.stopPropagation();
          close();
        }
      }
    });

    input.addEventListener("blur", function (e) {
      if (e.relatedTarget && (popup.contains(e.relatedTarget) || e.relatedTarget === btn)) return;
      close();
    });

    btn.addEventListener("mousedown", function (e) {
      e.preventDefault();
    });

    btn.addEventListener("click", function (e) {
      e.preventDefault();
      e.stopPropagation();
      if (isDisabled()) return;
      if (isOpen) {
        close();
        return;
      }
      suppressFocusOpen = true;
      try { input.focus({ preventScroll: true }); } catch (_) {}
      suppressFocusOpen = false;
      pendingOpen = "all";
      open(true);
    });

    popup.addEventListener("mousedown", function (e) {
      e.preventDefault();
    });

    popup.addEventListener("click", function (e) {
      var optEl = e.target && e.target.closest ? e.target.closest("li.scb-opt") : null;
      if (!optEl) return;
      e.preventDefault();
      e.stopPropagation();
      selectOption({ id: optEl.dataset.value });
    });

    return {
      node: wrap,
      input: input,
      button: btn,
      popup: popup,
      open: open,
      close: close,
      refresh: function () {
        btn.disabled = isDisabled();
        if (isDisabled()) {
          close();
        } else if (isOpen) {
          renderList();
        }
      }
    };
  }

  return {
    closeActive: function () {
      if (!activeClose) return false;
      activeClose();
      return true;
    },
    attach: attach,
    loadChoices: loadChoices,
    backendOptions: backendOptions,
    modelOptions: modelOptions,
    effortOptions: effortOptions,
    filterOptions: filterOptions,
    _resetForTest: function () {
      choicesPromise = null;
      choicesData = null;
      waiters = [];
      if (activeClose) activeClose();
    }
  };
})();
if (typeof window !== "undefined") window.WsSettingsCombobox = WsSettingsCombobox;
