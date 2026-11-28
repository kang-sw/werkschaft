// .gal-rail hover & title-drag-hover preview popover.
// Single reused .gal-pop (pointer-events: none, z-index 42) positioned via
// translate3d and clamped --caret-y. Docked pages render miniature split cards
// directly inside the outer popover frame with no outer header or ratio meta.
(function () {
  var doc = typeof document !== "undefined" ? document : null;
  var NS = "http://www.w3.org/2000/svg";

  function clamp(v, lo, hi) {
    return v < lo ? lo : v > hi ? hi : v;
  }

  function computePosition(dotRect, popRect, vw, vh) {
    var gap = 10, pad = 8;
    var side = "right";
    var x = dotRect.right + gap;
    if (x + popRect.width > vw - pad && dotRect.left - gap - popRect.width >= pad) {
      side = "left";
      x = dotRect.left - gap - popRect.width;
    } else if (x + popRect.width > vw - pad) {
      side = "left";
      x = Math.max(pad, dotRect.left - gap - popRect.width);
    }
    var dotCenterY = dotRect.top + dotRect.height / 2;
    var y = clamp(dotCenterY - popRect.height / 2, pad, Math.max(pad, vh - popRect.height - pad));
    var caretY = clamp(dotCenterY - y, 10, Math.max(10, popRect.height - 10));
    return { x: Math.round(x), y: Math.round(y), caretY: Math.round(caretY), side: side };
  }

  function makeSvgIcon(href, cls) {
    var svg = doc.createElementNS(NS, "svg");
    svg.setAttribute("class", cls);
    svg.setAttribute("aria-hidden", "true");
    var use = doc.createElementNS(NS, "use");
    use.setAttribute("href", href);
    svg.appendChild(use);
    return svg;
  }

  function mk(tag, cls, text) {
    var el = doc.createElement(tag);
    if (cls) el.className = cls;
    if (text !== undefined && text !== null) el.textContent = text;
    return el;
  }

  function createController() {
    var cfg = null;
    var pop = null;
    var bodyEl = null;
    var dropEl = null;
    var springWrap = null;
    var springBar = null;
    var springAnim = null;
    var shownDot = null;
    var shownKey = "";
    var dragMode = false;
    var lastDragDot = null;
    var lastDragToKey = "";
    var sprungSuppressedEl = null;
    var activeSpringEl = null;
    var activeSpringMs = 0;

    function t(key, fallback) {
      if (cfg && typeof cfg.t === "function") {
        var v = cfg.t(key);
        if (v && v !== key) return v;
      }
      return fallback || key;
    }

    function ensurePop() {
      if (pop || !doc || !doc.body) return pop;
      pop = mk("div", "gal-pop");
      pop.setAttribute("role", "tooltip");
      pop.hidden = true;
      bodyEl = mk("div", "gal-pop-body");
      dropEl = mk("div", "gal-pop-drop");
      dropEl.hidden = true;
      springWrap = mk("div", "gal-pop-spring");
      springWrap.hidden = true;
      var springLabel = mk("span", "gal-pop-spring-label");
      springLabel.textContent = t("js.gal.preview.spring", "Hold to open page");
      var track = mk("div", "gal-pop-spring-track");
      springBar = mk("div", "gal-pop-spring-bar");
      track.appendChild(springBar);
      springWrap.appendChild(springLabel);
      springWrap.appendChild(track);
      pop.appendChild(bodyEl);
      pop.appendChild(dropEl);
      pop.appendChild(springWrap);
      doc.body.appendChild(pop);
      return pop;
    }

    function cancelSpringAnim() {
      if (springAnim) {
        try { springAnim.cancel(); } catch (e) {}
        springAnim = null;
      }
      if (springWrap && !springWrap.hidden) springWrap.hidden = true;
      if (springBar) springBar.style.transform = "";
    }

    function applySpringBar(dotEl) {
      if (!springWrap || !springBar) return;
      if (!activeSpringEl || activeSpringEl !== dotEl || activeSpringMs <= 0) {
        cancelSpringAnim();
        return;
      }
      var label = springWrap.firstChild;
      if (label) label.textContent = t("js.gal.preview.spring", "Hold to open page");
      springWrap.hidden = false;
      if (springAnim) {
        try { springAnim.cancel(); } catch (e) {}
        springAnim = null;
      }
      if (typeof springBar.animate === "function") {
        springAnim = springBar.animate(
          [{ transform: "scaleX(0)" }, { transform: "scaleX(1)" }],
          { duration: activeSpringMs, easing: "linear", fill: "forwards" }
        );
      } else {
        springBar.style.transform = "scaleX(1)";
      }
    }

    function leafRef(node) {
      if (!node || typeof node !== "object") return null;
      if (typeof node.ref === "string" && node.ref) return node.ref;
      if (typeof node.card === "string" && node.card) return "c:" + node.card;
      if (typeof node.topic === "string" && node.topic) return "t:" + node.topic;
      return null;
    }

    var STATE_CODES = {
      implementing: "impl",
      researching: "research",
      discussing: "discuss",
      waiting: "wait",
      "awaiting-confirmation": "confirm",
      closed: "closed",
      card: "card"
    };

    function resolveStateCode(info) {
      if (info.stateCode) return info.stateCode;
      if (info.closed) return "closed";
      if (info.state && STATE_CODES[info.state]) return STATE_CODES[info.state];
      return "";
    }

    function renderNode(node, getLeafInfo) {
      if (!node) return mk("div", "gal-pop-card");
      var ref = leafRef(node);
      if (ref !== null) {
        var info = getLeafInfo(ref) || {};
        var isTopic = info.kind === "topic" || ref.slice(0, 2) === "t:";
        var stateCode = resolveStateCode(info);
        var card = mk("div", "gal-pop-card");
        if (info.closed) card.setAttribute("data-closed", "1");
        if (info.asks) card.setAttribute("data-ask", "1");
        if (stateCode) card.setAttribute("data-state", stateCode);
        var hd = mk("div", "gal-pop-card-hd");
        if (isTopic) {
          var dot = mk("span", "gal-pop-card-dot");
          dot.setAttribute("aria-hidden", "true");
          hd.appendChild(dot);
        } else if (info.iconHref) {
          hd.appendChild(makeSvgIcon(info.iconHref, "gal-pop-card-icon " + (info.iconState || "plain")));
        }
        hd.appendChild(mk("span", "gal-pop-card-title", info.title || ref));
        if (info.stateLabel) {
          hd.appendChild(mk("span", "gal-pop-card-state", info.stateLabel));
        }
        if (info.asks) {
          hd.appendChild(mk("span", "gal-pop-card-ask", info.asksCount || "!"));
        }
        card.appendChild(hd);
        if (info.sub) {
          card.appendChild(mk("div", "gal-pop-card-sub", info.sub));
        }
        return card;
      }
      var split = mk("div", "gal-pop-split " + (node.axis === "row" ? "gal-pop-row" : "gal-pop-col"));
      var ratio = typeof node.ratio === "number" && node.ratio > 0 && node.ratio < 1 ? node.ratio : 0.5;
      var a = renderNode(node.a, getLeafInfo);
      var b = renderNode(node.b, getLeafInfo);
      a.style.flexGrow = String(Math.round(ratio * 1000));
      b.style.flexGrow = String(Math.round((1 - ratio) * 1000));
      split.appendChild(a);
      split.appendChild(b);
      return split;
    }

    function dropActionKey(to) {
      if (!to || !to.gal) return "";
      return to.gal + (to.pos !== undefined ? ":" + to.pos : "");
    }

    function renderForDot(dotEl, isDrag, to) {
      if (!cfg || !dotEl || !doc) return;
      if (cfg.isOverview && cfg.isOverview()) { hide(); return; }
      var st = cfg.getState ? cfg.getState() : null;
      if (!st || !st.gal) { hide(); return; }
      var pos = parseInt(dotEl.getAttribute("data-pos"), 10);
      if (isNaN(pos)) { hide(); return; }
      var n = st.gal.pages ? st.gal.pages.length : 0;
      var toKey = isDrag ? dropActionKey(to) : "";
      var sysId = dotEl.getAttribute("data-sys") || "";
      var kind = sysId ? "sys" : pos < 0 ? "home" : pos >= n ? "plus" : "page";
      var pg = kind === "page" ? st.gal.pages[pos] : null;
      var renderKey = kind + "|" + (sysId || pos) + "|" + (pg ? pg.id : "") + "|" + (isDrag ? "d" : "h") + "|" + toKey;

      var p = ensurePop();
      if (!p) return;
      if (shownDot === dotEl && shownKey === renderKey && !p.hidden) {
        applySpringBar(dotEl);
        return;
      }

      // Read hovered dot geometry BEFORE writing popover DOM (<= 1 forced layout).
      var dotRect = dotEl.getBoundingClientRect();
      var vw = window.innerWidth || 1200;
      var vh = window.innerHeight || 800;

      bodyEl.textContent = "";
      if (kind === "sys") {
        var sinfo = cfg.getSysInfo ? (cfg.getSysInfo(sysId) || {}) : {};
        p.setAttribute("data-mode", "pill");
        var pillS = mk("div", "gal-pop-pill gal-pop-sys");
        if (sinfo.title) pillS.appendChild(mk("span", "gal-pop-pill-text", sinfo.title));
        else pillS.appendChild(mk("span", "gal-pop-pill-text", dotEl.getAttribute("aria-label") || sysId));
        if (sinfo.sub) pillS.appendChild(mk("span", "gal-pop-sys-sub", sinfo.sub));
        bodyEl.appendChild(pillS);
        dropEl.hidden = true;
      } else if (kind === "home") {
        p.setAttribute("data-mode", "pill");
        var pillH = mk("div", "gal-pop-pill");
        pillH.appendChild(mk("span", "gal-pop-pill-glyph", "⌂"));
        var homeText = isDrag && to && to.gal === "home"
          ? t("js.gal.preview.home.drag", "Undock to Home")
          : t("js.gal.preview.home", "Home");
        pillH.appendChild(mk("span", "gal-pop-pill-text", homeText));
        if (isDrag && to && to.gal === "home") pillH.setAttribute("data-gal-drop", "1");
        bodyEl.appendChild(pillH);
        dropEl.hidden = true;
      } else if (kind === "plus") {
        p.setAttribute("data-mode", "pill");
        var pillP = mk("div", "gal-pop-pill");
        pillP.appendChild(mk("span", "gal-pop-pill-glyph", "+"));
        var newText = isDrag && to && to.gal === "dock"
          ? t("js.gal.preview.new.drag", "Dock to new page")
          : t("js.gal.preview.new", "New page");
        pillP.appendChild(mk("span", "gal-pop-pill-text", newText));
        if (isDrag && to && to.gal === "dock") pillP.setAttribute("data-gal-drop", "1");
        bodyEl.appendChild(pillP);
        dropEl.hidden = true;
      } else {
        p.setAttribute("data-mode", "page");
        var layout = mk("div", "gal-pop-layout");
        layout.appendChild(renderNode(pg && pg.root, cfg.getLeafInfo || function () { return {}; }));
        bodyEl.appendChild(layout);
        if (isDrag && to && to.gal === "append") {
          dropEl.textContent = t("js.gal.preview.append", "Add split to this page");
          dropEl.hidden = false;
        } else {
          dropEl.hidden = true;
        }
      }

      applySpringBar(dotEl);
      if (p.hidden) p.hidden = false;
      shownDot = dotEl;
      shownKey = renderKey;

      // Single layout read after writing content to clamp Y and caret.
      var popRect = p.getBoundingClientRect();
      var geom = computePosition(dotRect, popRect, vw, vh);
      p.setAttribute("data-side", geom.side);
      p.style.transform = "translate3d(" + geom.x + "px, " + geom.y + "px, 0)";
      p.style.setProperty("--caret-y", geom.caretY + "px");
    }

    function hide() {
      shownDot = null;
      shownKey = "";
      cancelSpringAnim();
      if (pop && !pop.hidden) pop.hidden = true;
    }

    function onEscape(e) {
      if (e && e.key === "Escape" && pop && !pop.hidden) {
        // Never preventDefault or stopPropagation: preserve maximize & Overview Escape handlers.
        hide();
      }
    }

    function attach(options) {
      cfg = options || null;
      if (!cfg || !cfg.railEl || !cfg.dotsEl || !doc) return;
      var railEl = cfg.railEl, dotsEl = cfg.dotsEl;
      dotsEl.addEventListener("pointerover", function (e) {
        if (dragMode || (cfg.isDragging && cfg.isDragging())) return;
        var dot = e.target && e.target.closest && e.target.closest(".gal-dot");
        if (!dot) return;
        renderForDot(dot, false, null);
      });
      dotsEl.addEventListener("focusin", function (e) {
        if (dragMode || (cfg.isDragging && cfg.isDragging())) return;
        var dot = e.target && e.target.closest && e.target.closest(".gal-dot");
        if (!dot) return;
        renderForDot(dot, false, null);
      });
      railEl.addEventListener("pointerleave", function () {
        if (!dragMode) hide();
      });
      dotsEl.addEventListener("focusout", function (e) {
        if (!dragMode && !dotsEl.contains(e.relatedTarget)) hide();
      });
      dotsEl.addEventListener("scroll", function () {
        hide();
      }, { passive: true });
      doc.addEventListener("keydown", onEscape);
    }

    function dragOver(hitDot, to) {
      dragMode = true;
      var toKey = dropActionKey(to);
      if (hitDot !== sprungSuppressedEl) sprungSuppressedEl = null;
      if (!hitDot) {
        if (lastDragDot !== null || (pop && !pop.hidden)) {
          lastDragDot = null;
          lastDragToKey = "";
          hide();
        }
        return;
      }
      if (hitDot === sprungSuppressedEl) {
        lastDragDot = hitDot;
        lastDragToKey = toKey;
        return;
      }
      // 0 DOM writes & 0 layout reads when drag hit and target action are unchanged.
      if (hitDot === lastDragDot && toKey === lastDragToKey && pop && !pop.hidden) {
        return;
      }
      lastDragDot = hitDot;
      lastDragToKey = toKey;
      renderForDot(hitDot, true, to);
    }

    function dragEnd() {
      dragMode = false;
      lastDragDot = null;
      lastDragToKey = "";
      sprungSuppressedEl = null;
      activeSpringEl = null;
      activeSpringMs = 0;
      hide();
    }

    function springStart(dotEl, ms) {
      activeSpringEl = dotEl || null;
      activeSpringMs = ms || 0;
      if (dotEl && shownDot === dotEl && pop && !pop.hidden) {
        applySpringBar(dotEl);
      }
    }

    function springStop() {
      activeSpringEl = null;
      activeSpringMs = 0;
      cancelSpringAnim();
    }

    function sprung(dotEl) {
      activeSpringEl = null;
      activeSpringMs = 0;
      sprungSuppressedEl = dotEl || null;
      hide();
    }

    function sync() {
      if (shownDot && (!shownDot.isConnected || (cfg && cfg.isOverview && cfg.isOverview()))) {
        hide();
      }
    }

    return {
      attach: attach,
      dragOver: dragOver,
      dragEnd: dragEnd,
      springStart: springStart,
      springStop: springStop,
      sprung: sprung,
      hide: hide,
      sync: sync,
      computePosition: computePosition
    };
  }

  var api = createController();
  api.createController = createController;
  if (typeof window !== "undefined") window.WsGalPreview = api;
  if (typeof module !== "undefined" && module.exports) module.exports = api;
})();
