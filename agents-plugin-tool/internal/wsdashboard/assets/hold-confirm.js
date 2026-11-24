(function () {
  var DEFAULT_DURATION = 1500;
  var active = null;
  var suppressedEl = null;
  var suppressedTimer = 0;
  var lastPointerCancelAt = 0;

  function isInside(el, x, y) {
    if (!el || typeof el.getBoundingClientRect !== "function") return false;
    var r = el.getBoundingClientRect();
    if (!r || (r.width === 0 && r.height === 0)) return false;
    return x >= r.left && x <= r.right && y >= r.top && y <= r.bottom;
  }

  function defaultCanStart(el) {
    return !!el && !el.disabled && el.getAttribute("aria-disabled") !== "true";
  }

  function defaultShouldHold() {
    return true;
  }

  function suppressClick(el) {
    suppressedEl = el;
    if (suppressedTimer) clearTimeout(suppressedTimer);
    suppressedTimer = setTimeout(function () {
      suppressedEl = null;
      suppressedTimer = 0;
    }, 80);
  }

  function clearSuppression() {
    suppressedEl = null;
    if (suppressedTimer) {
      clearTimeout(suppressedTimer);
      suppressedTimer = 0;
    }
  }

  function clearVisuals(h) {
    if (!h) return;
    if (h.timer) {
      clearTimeout(h.timer);
      h.timer = 0;
    }
    if (h.el) {
      h.el.removeAttribute("data-hold-state");
      h.el.classList.remove("d-hold-active", "d-hold-ready");
      if (h.el.style) h.el.style.removeProperty("--d-hold-dur");
    }
    if (h.row) h.row.classList.remove("d-holding");
  }

  function cancelActive(fromPointer, reason) {
    if (!active) return;
    var h = active;
    active = null;
    if (fromPointer) lastPointerCancelAt = Date.now();
    clearVisuals(h);
    if (h.opts && typeof h.opts.onCancel === "function") h.opts.onCancel(h.el, reason || "cancel");
  }

  function startHold(el, opts, meta) {
    cancelActive(false, "replaced");
    var dur = typeof opts.duration === "number" && opts.duration >= 0 ? opts.duration : DEFAULT_DURATION;
    var row = el.closest ? el.closest(".rs") : null;
    var h = {
      el: el,
      opts: opts,
      row: row,
      source: meta.source,
      pointerId: meta.pointerId,
      key: meta.key,
      ready: false,
      timer: 0
    };
    active = h;
    el.setAttribute("data-hold-mode", opts.mode || "bar");
    el.setAttribute("data-hold-state", "holding");
    if (el.style) el.style.setProperty("--d-hold-dur", dur + "ms");
    el.classList.add("d-hold-active");
    el.classList.remove("d-hold-ready");
    if (row) row.classList.add("d-holding");
    if (typeof opts.onStart === "function") opts.onStart(el);
    h.timer = setTimeout(function () {
      if (active !== h) return;
      h.timer = 0;
      if (!el.isConnected || !(opts.canStart || defaultCanStart)(el)) {
        cancelActive(h.source === "pointer", "detached");
        return;
      }
      h.ready = true;
      el.setAttribute("data-hold-state", "ready");
      el.classList.add("d-hold-ready");
      if (typeof opts.onReady === "function") opts.onReady(el);
    }, dur);
  }

  function finishHold(h) {
    active = null;
    clearVisuals(h);
    if (h.opts && typeof h.opts.onConfirm === "function") h.opts.onConfirm(h.el, true);
  }

  if (typeof window !== "undefined") {
    window.addEventListener("pointermove", function (e) {
      if (!active || active.source !== "pointer") return;
      if (active.pointerId !== undefined && e.pointerId !== undefined && e.pointerId !== active.pointerId) return;
      var canStart = active.opts.canStart || defaultCanStart;
      if (!active.el.isConnected || !canStart(active.el) || !isInside(active.el, e.clientX, e.clientY)) {
        cancelActive(true, "leave");
      }
    }, true);

    window.addEventListener("pointerup", function (e) {
      if (!active || active.source !== "pointer") return;
      if (active.pointerId !== undefined && e.pointerId !== undefined && e.pointerId !== active.pointerId) return;
      var h = active;
      var canStart = h.opts.canStart || defaultCanStart;
      var inside = h.el.isConnected && canStart(h.el) && isInside(h.el, e.clientX, e.clientY);
      suppressClick(h.el);
      if (h.ready && inside) {
        finishHold(h);
      } else {
        cancelActive(true, !h.ready && inside ? "early-release" : "leave");
      }
    }, true);

    window.addEventListener("pointercancel", function (e) {
      if (!active || active.source !== "pointer") return;
      if (active.pointerId !== undefined && e.pointerId !== undefined && e.pointerId !== active.pointerId) return;
      cancelActive(true, "cancel");
    }, true);

    window.addEventListener("keydown", function (e) {
      if (!active || e.key !== "Escape") return;
      e.preventDefault();
      e.stopImmediatePropagation();
      cancelActive(false, "escape");
    }, true);

    window.addEventListener("keyup", function (e) {
      if (!active || active.source !== "key" || e.key !== active.key) return;
      e.preventDefault();
      var h = active;
      var canStart = h.opts.canStart || defaultCanStart;
      var alive = h.el.isConnected && canStart(h.el);
      suppressClick(h.el);
      if (h.ready && alive) {
        finishHold(h);
      } else {
        cancelActive(false, !h.ready && alive ? "early-release" : "cancel");
      }
    }, true);

    window.addEventListener("blur", function () {
      if (active) cancelActive(false, "blur");
    });
  }

  if (typeof document !== "undefined") {
    document.addEventListener("visibilitychange", function () {
      if (active && document.hidden) cancelActive(false, "hidden");
    });
    document.addEventListener("blur", function (e) {
      if (active && e.target === active.el) cancelActive(false, "blur");
    }, true);
  }

  function handlePointerDown(el, opts, e) {
    if (!el) return;
    if (typeof e.button === "number" && e.button !== 0) return;
    if (e.isPrimary === false) return;
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    var canStart = opts.canStart || defaultCanStart;
    if (!canStart(el)) return;
    var shouldHold = opts.shouldHold || defaultShouldHold;
    if (!shouldHold(el)) return;
    startHold(el, opts, { source: "pointer", pointerId: e.pointerId });
  }

  function handleKeyDown(el, opts, e) {
    if (!el) return;
    if (e.key !== " " && e.key !== "Enter") return;
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    var canStart = opts.canStart || defaultCanStart;
    if (!canStart(el)) return;
    var shouldHold = opts.shouldHold || defaultShouldHold;
    if (!shouldHold(el)) return;
    e.preventDefault();
    if (e.repeat) return;
    startHold(el, opts, { source: "key", key: e.key });
  }

  function handleClick(el, opts, e) {
    if (!el) return;
    if (suppressedEl === el) {
      clearSuppression();
      e.preventDefault();
      e.stopPropagation();
      return;
    }
    var canStart = opts.canStart || defaultCanStart;
    if (!canStart(el)) return;
    var shouldHold = opts.shouldHold || defaultShouldHold;
    if (shouldHold(el)) {
      e.preventDefault();
      e.stopPropagation();
      return;
    }
    e.preventDefault();
    if (typeof opts.onConfirm === "function") opts.onConfirm(el, false);
  }

  function attach(el, opts) {
    opts = opts || {};
    function sync() {
      if (active && active.el === el) cancelActive(false);
      var shouldHold = opts.shouldHold || defaultShouldHold;
      el.setAttribute("data-hold-mode", opts.mode || "bar");
      el.classList.toggle("d-hold-btn", !!shouldHold(el));
    }
    function onPointerDown(e) { handlePointerDown(el, opts, e); }
    function onPointerLeave() {
      if (active && active.el === el && active.source === "pointer") cancelActive(true, "leave");
    }
    function onKeyDown(e) { handleKeyDown(el, opts, e); }
    function onClick(e) { handleClick(el, opts, e); }

    sync();
    el.addEventListener("pointerdown", onPointerDown);
    el.addEventListener("pointerleave", onPointerLeave);
    el.addEventListener("keydown", onKeyDown);
    el.addEventListener("click", onClick);

    return {
      sync: sync,
      cancel: function () { if (active && active.el === el) cancelActive(false); },
      destroy: function () {
        if (active && active.el === el) cancelActive(false);
        el.removeEventListener("pointerdown", onPointerDown);
        el.removeEventListener("pointerleave", onPointerLeave);
        el.removeEventListener("keydown", onKeyDown);
        el.removeEventListener("click", onClick);
      }
    };
  }

  function delegate(container, selector, opts) {
    opts = opts || {};
    function findTarget(t) {
      return t && t.closest ? t.closest(selector) : null;
    }
    function onPointerDown(e) {
      var el = findTarget(e.target);
      if (el && container.contains(el)) handlePointerDown(el, opts, e);
    }
    function onPointerOut(e) {
      if (!active || active.source !== "pointer") return;
      var el = findTarget(e.target);
      if (!el || el !== active.el) return;
      if (!e.relatedTarget || !el.contains(e.relatedTarget)) cancelActive(true, "leave");
    }
    function onKeyDown(e) {
      var el = findTarget(e.target);
      if (el && container.contains(el)) handleKeyDown(el, opts, e);
    }
    function onClick(e) {
      var el = findTarget(e.target);
      if (el && container.contains(el)) handleClick(el, opts, e);
    }

    container.addEventListener("pointerdown", onPointerDown);
    container.addEventListener("pointerout", onPointerOut);
    container.addEventListener("keydown", onKeyDown);
    container.addEventListener("click", onClick);

    return {
      cancel: function () {
        if (active && container.contains(active.el)) cancelActive(false);
      },
      destroy: function () {
        if (active && container.contains(active.el)) cancelActive(false);
        container.removeEventListener("pointerdown", onPointerDown);
        container.removeEventListener("pointerout", onPointerOut);
        container.removeEventListener("keydown", onKeyDown);
        container.removeEventListener("click", onClick);
      }
    };
  }

  var api = {
    DURATION: DEFAULT_DURATION,
    attach: attach,
    delegate: delegate,
    isHolding: function () { return !!active; },
    cancelAll: function () { cancelActive(false); },
    wasJustCancelled: function () { return Date.now() - lastPointerCancelAt < 120; }
  };

  if (typeof window !== "undefined") window.WsHoldConfirm = api;
  if (typeof module !== "undefined" && module.exports) module.exports = api;
}());
