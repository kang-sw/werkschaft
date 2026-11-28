(function (C) {
  "use strict";

  // Stage speech bubble controller (.ws-companion-quip) for Puppeteer side-comments.
  // - Strict textContent-only assignment (zero innerHTML).
  // - Enforces the 140-rune (Unicode code point) cap matching companion_command.go.
  // - Head-relative anchoring using projected VRM bounds (rig.getCharacterBounds)
  //   with fallback to bodyBox / 2D coordinates, clamped cleanly inside the viewport
  //   and above .shell-bottom.
  // - Computes --quip-tail-x so the CSS directional speech tail points directly at
  //   the character's head even when the bubble is clamped near a viewport edge.
  const MAX_RUNES = 140;
  const VIEWPORT_PAD = 8;
  const HEAD_GAP = 10;
  const TAIL_INSET = 14;
  const DEFAULT_SHELL_BOTTOM = 32;

  function clamp(v, lo, hi) {
    if (hi < lo) return lo;
    return v < lo ? lo : v > hi ? hi : v;
  }

  function sanitize(raw) {
    if (typeof raw !== "string") return "";
    const folded = raw.replace(/[\u0000-\u001f\u007f]+/g, " ").replace(/\s+/g, " ").trim();
    if (!folded) return "";
    const runes = Array.from(folded);
    return runes.length > MAX_RUNES ? runes.slice(0, MAX_RUNES).join("") : folded;
  }

  function measureShellBottom(doc) {
    try {
      const bar = doc?.getElementById?.("shell-bottom") || doc?.querySelector?.(".shell-bottom");
      if (bar && !bar.hidden && typeof bar.getBoundingClientRect === "function") {
        const r = bar.getBoundingClientRect();
        if (r && Number.isFinite(r.height) && r.height > 0) return Math.ceil(r.height) + 6;
      }
    } catch (_) { /* fake DOM */ }
    return DEFAULT_SHELL_BOTTOM;
  }

  function create(parentNode, doc) {
    let el = null;
    let width = 0;
    let height = 28;
    let currentText = "";
    let cachedBottom = DEFAULT_SHELL_BOTTOM;
    let cachedBottomVh = -1;

    function ensureElement() {
      if (el || !parentNode || !doc) return el;
      el = doc.createElement("div");
      el.className = "ws-companion-quip";
      el.setAttribute("role", "status");
      el.setAttribute("aria-live", "polite");
      el.dataset.placement = "above";
      el.dataset.open = "0";
      el.hidden = true;
      parentNode.append(el);
      return el;
    }

    function set(rawText) {
      const text = sanitize(rawText);
      if (!text) {
        hide();
        return false;
      }
      const node = ensureElement();
      if (!node) return false;
      const changed = text !== currentText || node.hidden;
      currentText = text;
      // Strict textContent-only assignment: model output must never become HTML.
      node.textContent = text;
      node.hidden = false;
      node.dataset.open = "1";
      if (changed) {
        width = node.offsetWidth || 0;
        height = node.offsetHeight || 28;
        cachedBottomVh = -1;
      }
      return true;
    }

    function hide() {
      currentText = "";
      if (!el) return;
      el.dataset.open = "0";
      el.hidden = true;
    }

    function position(ctx) {
      if (!el || el.hidden || !ctx) return null;
      const vw = Number.isFinite(ctx.vw) && ctx.vw > 0 ? ctx.vw : 1280;
      const vh = Number.isFinite(ctx.vh) && ctx.vh > 0 ? ctx.vh : 800;
      // Reuse dimensions measured on text change so steady-state frames avoid
      // per-frame offsetWidth/getBoundingClientRect forced layout reads.
      if (!width) {
        width = el.offsetWidth || 160;
        height = el.offsetHeight || 28;
      }
      const w = width;
      const h = height || 28;

      let bottomReserve;
      if (Number.isFinite(ctx.bottomInset)) {
        bottomReserve = ctx.bottomInset;
      } else {
        if (cachedBottomVh !== vh) {
          cachedBottom = measureShellBottom(doc);
          cachedBottomVh = vh;
        }
        bottomReserve = cachedBottom;
      }
      let anchorX = Number.isFinite(ctx.position?.x) ? ctx.position.x : vw * 0.5;
      let anchorTop = 0;
      let anchorBottom = 0;

      if (ctx.isVrm) {
        const pb = ctx.projectedBounds;
        const bb = ctx.bodyBox;
        if (pb && Number.isFinite(pb.viewportX) && Number.isFinite(pb.viewportY) && pb.width > 0 && pb.height > 0) {
          anchorX = pb.viewportX + pb.width * 0.5;
          anchorTop = pb.viewportY;
          anchorBottom = pb.viewportY + pb.height;
        } else if (bb && Number.isFinite(bb.x) && Number.isFinite(bb.y)) {
          anchorX = bb.x + (bb.width || 0) * 0.5;
          anchorTop = bb.y;
          anchorBottom = bb.y + (bb.height || ctx.vrmHeight || 0);
        } else {
          anchorTop = (ctx.position?.y || 120) - (ctx.vrmHeight || 160);
          anchorBottom = ctx.position?.y || 120;
        }
      } else {
        const charH = Number.isFinite(ctx.size) ? ctx.size : 96;
        anchorTop = (ctx.position?.y || 120) - charH * 0.95;
        anchorBottom = ctx.position?.y || 120;
      }

      const maxLeft = Math.max(VIEWPORT_PAD, vw - w - VIEWPORT_PAD);
      const left = Math.round(clamp(anchorX - w / 2, VIEWPORT_PAD, maxLeft));

      const maxTop = Math.max(VIEWPORT_PAD, vh - bottomReserve - h - VIEWPORT_PAD);
      let placement = "above";
      let rawTop = anchorTop - h - HEAD_GAP;
      if (rawTop < VIEWPORT_PAD && anchorBottom + HEAD_GAP <= maxTop) {
        placement = "below";
        rawTop = anchorBottom + HEAD_GAP;
      }
      const top = Math.round(clamp(rawTop, VIEWPORT_PAD, maxTop));
      const tailX = Math.round(clamp(anchorX - left, TAIL_INSET, Math.max(TAIL_INSET, w - TAIL_INSET)));

      el.dataset.placement = placement;
      el.style.setProperty?.("--quip-tail-x", tailX + "px");
      el.style.transform = "translate3d(" + left + "px," + top + "px,0)";
      return { left, top, width: w, height: h, tailX, placement };
    }

    function dispose() {
      currentText = "";
      if (el) {
        el.remove?.();
        el = null;
      }
    }

    return {
      set,
      hide,
      position,
      dispose,
      element() { return el; },
      isVisible() { return !!(el && !el.hidden); }
    };
  }

  C.quip = {
    MAX_RUNES,
    sanitize,
    create
  };
})(window.WsCompanion);
