  // --- Variant D IDE shell: resizable/collapsible left rail & frame sync -------
  // Persists rail width (220px-360px, default 280px) and wide-mode collapsed
  // state in localStorage, drives the RAF-throttled #rail-splitter drag and
  // keyboard resize via CSS custom property --rail-w (no per-frame storage
  // writes or layout thrash), and pushes live width/collapsed state to framed
  // pages over the existing shell-to-frame message channel ("rail-layout").
  var IDE_RAIL_MIN = 220, IDE_RAIL_MAX = 360, IDE_RAIL_DEFAULT = 280;
  var IDE_RAIL_WIDTH_KEY = "ws.dashboard.railWidth";
  var IDE_RAIL_COLLAPSED_KEY = "ws.dashboard.railCollapsed";

  function ideRailClamp(v) {
    var n = typeof v === "number" ? v : parseInt(v, 10);
    if (!isFinite(n)) return IDE_RAIL_DEFAULT;
    return Math.max(IDE_RAIL_MIN, Math.min(IDE_RAIL_MAX, Math.round(n)));
  }

  var ideRailWidth = ideRailClamp(load(IDE_RAIL_WIDTH_KEY));
  var ideRailCollapsed = load(IDE_RAIL_COLLAPSED_KEY) === "1";
  var ideSplitterEl = $("rail-splitter");
  var ideFrameQueueHead = null;

  function ideRailApplyDOM(w, collapsed) {
    ideRailWidth = ideRailClamp(w);
    ideRailCollapsed = !!collapsed;
    root.style.setProperty("--rail-w", ideRailWidth + "px");
    if (ideRailCollapsed) root.setAttribute("data-rail-collapsed", "true");
    else root.removeAttribute("data-rail-collapsed");
    if (ideSplitterEl) {
      ideSplitterEl.setAttribute("aria-valuenow", String(ideRailWidth));
      ideSplitterEl.setAttribute("aria-valuemin", String(IDE_RAIL_MIN));
      ideSplitterEl.setAttribute("aria-valuemax", String(IDE_RAIL_MAX));
    }
    if (shell && railBtn && railWide()) {
      railBtn.setAttribute("aria-expanded", String(!ideRailCollapsed));
    }
  }

  function ideRailNotifyFrames(targetEnt) {
    if (!shell || typeof shSend !== "function") return;
    var payload = { width: ideRailWidth, collapsed: ideRailCollapsed };
    if (targetEnt) {
      shSend(targetEnt, "rail-layout", payload);
      return;
    }
    if (typeof shFrames !== "undefined" && Array.isArray(shFrames)) {
      shFrames.forEach(function (ent) { shSend(ent, "rail-layout", payload); });
    }
  }

  function ideRailSetWidth(w, persist) {
    var next = ideRailClamp(w);
    var changed = next !== ideRailWidth;
    ideRailApplyDOM(next, ideRailCollapsed);
    if (persist) save(IDE_RAIL_WIDTH_KEY, String(ideRailWidth));
    if (changed || persist) ideRailNotifyFrames();
  }

  function ideRailSetCollapsed(collapsed, persist) {
    var next = !!collapsed;
    var changed = next !== ideRailCollapsed;
    ideRailApplyDOM(ideRailWidth, next);
    if (persist) {
      if (next) save(IDE_RAIL_COLLAPSED_KEY, "1");
      else save(IDE_RAIL_COLLAPSED_KEY, null);
    }
    if (next && typeof rp !== "undefined" && rp && rp.P && typeof railPopClose === "function") {
      railPopClose(false);
    }
    if (changed || persist) ideRailNotifyFrames();
  }

  function ideRailToggleCollapse() {
    ideRailSetCollapsed(!ideRailCollapsed, true);
  }

  function ideRailApplyFromShell(w, collapsed) {
    var nextW = ideRailClamp(w);
    var nextCol = !!collapsed;
    var changed = nextW !== ideRailWidth || nextCol !== ideRailCollapsed ||
      root.style.getPropertyValue("--rail-w") !== nextW + "px" ||
      (root.getAttribute("data-rail-collapsed") === "true") !== nextCol;
    ideRailApplyDOM(nextW, nextCol);
    if (!changed) return;
    if (window.WsCards && typeof window.WsCards.relayout === "function") window.WsCards.relayout();
    if (typeof queueGallery === "function") queueGallery();
    if (typeof ideFrameQueueHead === "function") ideFrameQueueHead();
  }

  if (shell || framed) {
    ideRailApplyDOM(ideRailWidth, ideRailCollapsed);
  }

  if (shell) {
    var ideQsKbd = $("tkqs-kbd"), ideQsBtn = $("tkqs");
    var ideIsMac = typeof navigator !== "undefined" && /Mac|iPhone|iPad|iPod/i.test(navigator.platform || navigator.userAgent || "");
    if (ideQsKbd && ideIsMac) ideQsKbd.textContent = "\u2318K";
    if (ideQsBtn && ideIsMac && ideQsBtn.title) ideQsBtn.title = ideQsBtn.title.replace("(Ctrl+K)", "(\u2318K)");

    window.WsIdeShell = {
      min: IDE_RAIL_MIN,
      max: IDE_RAIL_MAX,
      def: IDE_RAIL_DEFAULT,
      getWidth: function () { return ideRailWidth; },
      setWidth: function (w) { ideRailSetWidth(w, true); },
      isCollapsed: function () { return ideRailCollapsed; },
      setCollapsed: function (c) { ideRailSetCollapsed(c, true); },
      toggleCollapse: ideRailToggleCollapse
    };

    window.addEventListener("storage", function (e) {
      if (!e || e.key === null || e.key === IDE_RAIL_WIDTH_KEY || e.key === IDE_RAIL_COLLAPSED_KEY) {
        var nextW = ideRailClamp(load(IDE_RAIL_WIDTH_KEY));
        var nextCol = load(IDE_RAIL_COLLAPSED_KEY) === "1";
        if (nextW !== ideRailWidth || nextCol !== ideRailCollapsed) {
          ideRailApplyDOM(nextW, nextCol);
          ideRailNotifyFrames();
        }
      }
    });

    if (ideSplitterEl) {
      var ideDrag = null, ideDragRaf = 0, ideDragPendingW = 0;

      function ideFlushDrag() {
        ideDragRaf = 0;
        if (!ideDrag) return;
        ideRailApplyDOM(ideDragPendingW, false);
        ideRailNotifyFrames(typeof shOn !== "undefined" ? shOn : null);
      }

      function ideEndDrag(commit) {
        if (!ideDrag) return;
        var d = ideDrag;
        ideDrag = null;
        if (ideDragRaf) {
          cancelAnimationFrame(ideDragRaf);
          ideDragRaf = 0;
        }
        root.removeAttribute("data-rail-resizing");
        try {
          if (ideSplitterEl.hasPointerCapture && ideSplitterEl.hasPointerCapture(d.id)) {
            ideSplitterEl.releasePointerCapture(d.id);
          }
        } catch (x) { /* ignore */ }
        if (commit) {
          ideRailSetWidth(ideDragPendingW, true);
        } else {
          ideRailSetWidth(d.startW, false);
        }
      }

      ideSplitterEl.addEventListener("pointerdown", function (e) {
        if (e.button !== 0 || !railWide() || ideRailCollapsed) return;
        e.preventDefault();
        ideDrag = { id: e.pointerId, startX: e.clientX, startW: ideRailWidth };
        ideDragPendingW = ideRailWidth;
        root.setAttribute("data-rail-resizing", "");
        try { ideSplitterEl.setPointerCapture(e.pointerId); } catch (x) { /* fallback to window */ }
      });

      window.addEventListener("pointermove", function (e) {
        if (!ideDrag || e.pointerId !== ideDrag.id) return;
        var next = ideRailClamp(ideDrag.startW + (e.clientX - ideDrag.startX));
        if (next === ideDragPendingW) return;
        ideDragPendingW = next;
        if (!ideDragRaf) ideDragRaf = requestAnimationFrame(ideFlushDrag);
      });

      window.addEventListener("pointerup", function (e) {
        if (!ideDrag || e.pointerId !== ideDrag.id) return;
        ideEndDrag(true);
      });

      window.addEventListener("pointercancel", function (e) {
        if (!ideDrag || e.pointerId !== ideDrag.id) return;
        ideEndDrag(false);
      });

      ideSplitterEl.addEventListener("dblclick", function (e) {
        if (!railWide() || ideRailCollapsed) return;
        e.preventDefault();
        ideRailSetWidth(IDE_RAIL_DEFAULT, true);
      });

      ideSplitterEl.addEventListener("keydown", function (e) {
        if (!railWide() || ideRailCollapsed) return;
        var step = e.shiftKey ? 20 : 10;
        if (e.key === "ArrowLeft") {
          e.preventDefault();
          ideRailSetWidth(ideRailWidth - step, true);
        } else if (e.key === "ArrowRight") {
          e.preventDefault();
          ideRailSetWidth(ideRailWidth + step, true);
        } else if (e.key === "Home") {
          e.preventDefault();
          ideRailSetWidth(IDE_RAIL_MIN, true);
        } else if (e.key === "End") {
          e.preventDefault();
          ideRailSetWidth(IDE_RAIL_MAX, true);
        } else if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          ideRailSetWidth(IDE_RAIL_DEFAULT, true);
        }
      });
    }
  }
