  // --- daemon interactive guide: highlight overlay, stop chip, monitor hooks --
  // ws/daemon.highlight draws a non-mutating spotlight / pulse / ring plus an
  // optional comment card over one element of the local top-level shell, the
  // active session frame, or the settings frame. The overlay is one fixed
  // shell-level layer (never inside an iframe, so nothing is clipped); frame
  // targets are projected through their iframe rect. Tracking is event driven
  // (scroll / resize / ResizeObserver / throttled MutationObserver) with at
  // most one rAF per change and no continuous loop. ws/daemon.monitor pushes
  // decide whether structural hooks (tab, section, modal, highlight click) send
  // guide-monitor-event frames; they never carry typed text or a route change.
  var DG_MAX_SLOTS = 8;
  var DG_EVENT_DEBOUNCE_MS = 300;
  var DG_MUTATION_THROTTLE_MS = 120;
  var DG_TEXT_SCAN_MAX = 6000;
  var DG_FIELD_MAX = 64;
  var DG_ROUTE_MAX = 256;
  var DG_PAD = 4;
  var DG_TEXT_SKIP = { SCRIPT: 1, STYLE: 1, NOSCRIPT: 1, TEMPLATE: 1, LINK: 1, META: 1, IFRAME: 1, svg: 1, SVG: 1 };
  var DG_TEXT_BUTTON_INPUTS = { button: 1, submit: 1, reset: 1 };
  var DG_RERESOLVE_MS = 1000;
  var DG_MONITOR_EVENTS = { tab_switched: 1, section_changed: 1, modal_opened: 1, modal_closed: 1, highlight_clicked: 1 };

  // Frame side: the shell resolves a daemon.dom ref of this framed page by a
  // direct same-origin call (an Element cannot cross postMessage).
  if (framed) {
    window.wsDaemonGuideRef = function (gen, ref) {
      var g = Number(gen) || 0;
      if (!dcFrameGen || (g && g !== dcFrameGen)) return { error: "stale_ref" };
      var el = dcFrameRefs.get(String(ref || ""));
      if (!el) return { error: "ref_not_found" };
      if (!el.isConnected) return { error: "stale_ref" };
      return { el: el };
    };
  }

  if (shell && daemonBridgeEnabled) (function () {
    var slots = new Map(); // id -> slot, insertion order = show order
    var layer = null, chip = null, raf = 0, rafCount = 0, mutTimer = 0;
    var winHooks = new Map(); // Window -> {off}
    var monitor = { active: false, ids: [], events: {}, stopped: {}, timer: 0 };
    var monHooks = []; // [{doc, mo, kind, last}]
    var pending = {}, pendingTimers = {};

    function clampStr(s, max) {
      s = String(s == null ? "" : s).replace(/[\u0000-\u001f\u007f]/g, "").trim();
      return s.length > max ? s.slice(0, max) : s;
    }
    function normText(s) { return String(s || "").replace(/\s+/g, " ").trim().toLowerCase(); }

    // ---- layer ---------------------------------------------------------------
    // The modal settings dialog makes everything outside it inert, so the layer
    // lives inside the dialog while it is open and in <body> otherwise.
    function layerHost() {
      return shSettingsDialog && shSettingsDialog.open ? shSettingsDialog : doc.body;
    }
    function placeLayer() {
      if (!layer) return;
      var host = layerHost();
      if (host && layer.parentNode !== host) host.appendChild(layer);
    }
    function ensureLayer() {
      if (layer) return layer;
      layer = doc.createElement("div");
      layer.className = "dg-layer";
      layer.setAttribute("data-ws-dom-exclude", "1");
      placeLayer();
      return layer;
    }

    function buildSlotNodes(slot) {
      var spot = doc.createElement("div");
      spot.className = "dg-spot";
      spot.hidden = true;
      var card = doc.createElement("div");
      card.className = "dg-card";
      card.setAttribute("role", "note");
      card.hidden = true;
      var text = doc.createElement("p");
      text.className = "dg-card-text";
      var close = doc.createElement("button");
      close.type = "button";
      close.className = "dg-card-close";
      close.setAttribute("aria-label", wsT("js.dc.guide.dismiss"));
      close.textContent = "×";
      close.addEventListener("click", function (e) {
        e.stopPropagation();
        clearSlot(slot.id);
        syncHooks();
      });
      card.addEventListener("click", function (e) {
        if (e.target === close) return;
        emit("highlight_clicked", { highlight_id: slot.id });
      });
      card.appendChild(text);
      card.appendChild(close);
      ensureLayer().appendChild(spot);
      layer.appendChild(card);
      slot.spot = spot;
      slot.card = card;
      slot.text = text;
    }

    function applySlotLook(slot) {
      slot.spot.setAttribute("data-style", slot.style);
      slot.spot.removeAttribute("data-anim");
      slot.text.textContent = slot.comment;
      slot.card.toggleAttribute("data-empty", !slot.comment);
      // Restart the finite entry pulse; CSS stops it after a few iterations.
      void slot.spot.offsetWidth;
      slot.spot.setAttribute("data-anim", "1");
    }

    // ---- target resolution ---------------------------------------------------
    function entryDoc(entry) {
      try { return entry && entry.el && entry.el.contentDocument && entry.el.contentDocument.body ? entry.el.contentDocument : null; } catch (_) { return null; }
    }
    function scopes() {
      var out = [{ scope: "shell", doc: doc, win: window, frameEl: null }];
      var fd = shOn && entryDoc(shOn);
      if (fd) out.push({ scope: "frame", doc: fd, win: fd.defaultView, frameEl: shOn.el });
      var sd = shSettingsEntry && entryDoc(shSettingsEntry);
      if (sd) out.push({ scope: "settings", doc: sd, win: sd.defaultView, frameEl: shSettingsEntry.el });
      return out;
    }
    function ownLayer(el) { return !!(layer && (el === layer || layer.contains(el))); }

    function bySelector(sc, sel, acc) {
      var list;
      try { list = sc.doc.querySelectorAll(sel); } catch (_) { acc.invalid = true; return null; }
      for (var i = 0; i < list.length; i++) {
        var el = list[i];
        if (ownLayer(el) || !dcIsVisible(el)) continue;
        if (dcIsExcluded(el)) { acc.excluded = true; continue; }
        return el;
      }
      return null;
    }
    function byText(sc, needle, exact, acc) {
      var list = sc.doc.body.getElementsByTagName("*");
      var n = Math.min(list.length, DG_TEXT_SCAN_MAX);
      for (var i = 0; i < n; i++) {
        var el = list[i];
        if (DG_TEXT_SKIP[el.tagName] || ownLayer(el)) continue;
        // Never match on user-editable content (drafts, inputs): the
        // found/not-found answer would otherwise leak its substrings.
        if (el.isContentEditable || el.tagName === "TEXTAREA" || el.tagName === "SELECT" || dcIsSensitiveInput(el)) continue;
        if (el.tagName === "INPUT" && !DG_TEXT_BUTTON_INPUTS[String(el.type || "").toLowerCase()]) continue;
        var hit = false;
        var direct = normText(dcDirectText(el));
        if (direct && (exact ? direct === needle : direct.indexOf(needle) >= 0)) hit = true;
        else if (dcIsInteractive(el, dcSemanticRole(el))) {
          var name = normText(dcAccessibleName(el, sc.doc));
          if (name && (exact ? name === needle : name.indexOf(needle) >= 0)) hit = true;
        }
        if (!hit || !dcIsVisible(el)) continue;
        if (dcIsExcluded(el)) { acc.excluded = true; continue; }
        return el;
      }
      return null;
    }
    function byRef(ref, gen) {
      if (dcShellRefs.has(ref)) {
        if (!dcShellGen || (gen && gen !== dcShellGen)) return { error: "stale_ref" };
        var el = dcShellRefs.get(ref);
        if (!el || !el.isConnected) return { error: "stale_ref" };
        return { el: el, sc: { scope: "shell", doc: doc, win: window, frameEl: null } };
      }
      var kind = dcRemoteRefTargets.get(ref);
      if (!kind) return { error: "ref_not_found" };
      var entry = kind === "frame" ? shOn : kind === "settings" ? shSettingsEntry : null;
      var d = entryDoc(entry);
      if (!d) return { error: "stale_ref" };
      var w = d.defaultView, res = null;
      try { res = w && typeof w.wsDaemonGuideRef === "function" ? w.wsDaemonGuideRef(gen, ref) : null; } catch (_) { res = null; }
      if (!res) return { error: "stale_ref" };
      if (res.error) return { error: String(res.error) };
      return { el: res.el, sc: { scope: kind, doc: d, win: w, frameEl: entry.el } };
    }
    // resolve returns {el, sc} or {error}. onlyScope re-resolves a detached
    // selector/text target inside the scope it was first found in.
    function resolve(target, onlyScope) {
      var kind = String(target.kind || "");
      if (kind === "ref") {
        var r = byRef(String(target.ref || ""), Number(target.generation) || 0);
        if (r.el && dcIsExcluded(r.el)) return { error: "target_excluded" };
        return r;
      }
      var acc = { excluded: false, invalid: false };
      var list = scopes().filter(function (sc) { return !onlyScope || sc.scope === onlyScope; });
      var i, el;
      if (kind === "selector") {
        var sel = String(target.selector || "");
        for (i = 0; i < list.length; i++) {
          el = bySelector(list[i], sel, acc);
          if (acc.invalid) return { error: "invalid_request:invalid_selector" };
          if (el) return { el: el, sc: list[i] };
        }
      } else if (kind === "text") {
        var needle = normText(target.text);
        if (!needle) return { error: "invalid_request:empty_text" };
        for (var pass = 0; pass < 2; pass++) {
          for (i = 0; i < list.length; i++) {
            el = byText(list[i], needle, pass === 0, acc);
            if (el) return { el: el, sc: list[i] };
          }
        }
      } else {
        return { error: "invalid_request:unknown_target_kind" };
      }
      return { error: acc.excluded ? "target_excluded" : "target_not_found" };
    }

    // ---- projection & tracking ---------------------------------------------
    function project(slot) {
      var el = slot.el;
      var vw = window.innerWidth, vh = window.innerHeight;
      if (!el || !el.isConnected) return null;
      var r = el.getBoundingClientRect();
      var x = r.left, y = r.top, w = r.width, h = r.height;
      var cl = 0, ct = 0, cr = vw, cb = vh;
      if (slot.frameEl) {
        if (!slot.frameEl.isConnected || !dcIsVisible(slot.frameEl)) return { x: 0, y: 0, width: 0, height: 0, visible: false };
        var fr = slot.frameEl.getBoundingClientRect();
        var ox = fr.left + slot.frameEl.clientLeft, oy = fr.top + slot.frameEl.clientTop;
        x += ox;
        y += oy;
        cl = Math.max(0, ox);
        ct = Math.max(0, oy);
        cr = Math.min(vw, ox + slot.frameEl.clientWidth);
        cb = Math.min(vh, oy + slot.frameEl.clientHeight);
      }
      var visible = w > 0 && h > 0 && x < cr && y < cb && x + w > cl && y + h > ct && dcIsVisible(el);
      return { x: Math.round(x), y: Math.round(y), width: Math.round(w), height: Math.round(h), visible: visible };
    }

    function gaze(slot, visible, rect) {
      var r = rect || { x: 0, y: 0, width: 0, height: 0 };
      try {
        doc.dispatchEvent(new CustomEvent("ws:guide-highlight-change", {
          detail: { id: slot.id, visible: !!visible, style: slot.style, rect: { x: r.x, y: r.y, width: r.width, height: r.height }, has_comment: !!slot.comment }
        }));
      } catch (_) {}
    }

    function placeCard(slot, p) {
      var card = slot.card;
      card.hidden = false;
      var vw = window.innerWidth, vh = window.innerHeight;
      var cw = card.offsetWidth, ch = card.offsetHeight;
      var top = p.y + p.height + DG_PAD + 8;
      if (top + ch > vh - 8) top = p.y - DG_PAD - 8 - ch;
      if (top < 8) top = Math.max(8, Math.min(vh - ch - 8, p.y + p.height + DG_PAD + 8));
      var left = Math.max(8, Math.min(vw - cw - 8, p.x));
      card.style.transform = "translate(" + Math.round(left) + "px," + Math.round(top) + "px)";
    }

    function update(slot) {
      // A detached selector/text target re-resolves at most once per
      // DG_RERESOLVE_MS, so a page that keeps mutating while the target is
      // absent does not rescan the document on every tick.
      if ((!slot.el || !slot.el.isConnected) && slot.target.kind !== "ref") {
        var now = Date.now(), wait = slot.resolvedAt ? slot.resolvedAt + DG_RERESOLVE_MS - now : 0;
        if (wait <= 0) {
          slot.resolvedAt = now;
          var again = resolve(slot.target, slot.scope);
          if (again.el) bindTarget(slot, again.el, again.sc);
        } else if (!slot.retry) {
          // One trailing retry, so a target that reappears inside the window
          // is picked up even if no further mutation arrives.
          slot.retry = setTimeout(function () {
            slot.retry = 0;
            if (slots.get(slot.id) === slot && !hiddenNow()) update(slot);
          }, wait);
        }
      }
      var p = project(slot) || { x: 0, y: 0, width: 0, height: 0, visible: false };
      var was = slot.last;
      p.vw = window.innerWidth;
      p.vh = window.innerHeight;
      var changed = !was || was.visible !== p.visible || was.x !== p.x || was.y !== p.y || was.width !== p.width || was.height !== p.height || was.vw !== p.vw || was.vh !== p.vh;
      slot.last = p;
      if (!changed) return p;
      if (!p.visible) {
        slot.spot.hidden = true;
        slot.card.hidden = true;
      } else {
        var s = slot.spot.style;
        slot.spot.hidden = false;
        s.width = (p.width + DG_PAD * 2) + "px";
        s.height = (p.height + DG_PAD * 2) + "px";
        s.transform = "translate(" + (p.x - DG_PAD) + "px," + (p.y - DG_PAD) + "px)";
        placeCard(slot, p);
      }
      gaze(slot, p.visible, p);
      return p;
    }

    // Tracking pauses while the page is hidden; the ws-page-visibility
    // handler re-schedules once it is shown again.
    function hiddenNow() { return typeof pageHidden === "function" ? pageHidden() : !!doc.hidden; }
    function schedule() {
      if (raf || !slots.size || hiddenNow()) return;
      rafCount++;
      raf = requestAnimationFrame(function () {
        raf = 0;
        slots.forEach(update);
      });
    }
    // A DOM mutation re-measures from a throttled timer instead of a rAF, so
    // a page that keeps mutating (a ticking clock) never keeps frames
    // scheduled; update() writes styles only when the projection changed.
    function scheduleSoon() {
      if (mutTimer || !slots.size || hiddenNow()) return;
      mutTimer = setTimeout(function () {
        mutTimer = 0;
        if (!raf) slots.forEach(update);
      }, DG_MUTATION_THROTTLE_MS);
    }

    function unbindTarget(slot) {
      if (slot.ro) { try { slot.ro.disconnect(); } catch (_) {} slot.ro = null; }
      if (slot.mo) { try { slot.mo.disconnect(); } catch (_) {} slot.mo = null; }
      if (slot.retry) { clearTimeout(slot.retry); slot.retry = 0; }
      slot.resolvedAt = 0;
    }
    function bindTarget(slot, el, sc) {
      unbindTarget(slot);
      slot.el = el;
      slot.scope = sc.scope;
      slot.win = sc.win;
      slot.frameEl = sc.frameEl;
      var w = sc.win || window;
      try {
        var RO = w.ResizeObserver || window.ResizeObserver;
        if (RO) { slot.ro = new RO(schedule); slot.ro.observe(el); }
      } catch (_) { slot.ro = null; }
      try {
        var MO = w.MutationObserver || window.MutationObserver;
        var body = sc.doc && sc.doc.body;
        if (MO && body) {
          slot.mo = new MO(function (recs) {
            for (var i = 0; i < recs.length; i++) if (!ownLayer(recs[i].target)) { scheduleSoon(); return; }
          });
          slot.mo.observe(body, { childList: true, subtree: true, attributes: true, attributeFilter: ["style", "class", "hidden", "open"] });
        }
      } catch (_) { slot.mo = null; }
    }

    function clearSlot(id) {
      var slot = slots.get(id);
      if (!slot) return 0;
      slots.delete(id);
      unbindTarget(slot);
      if (slot.spot) slot.spot.remove();
      if (slot.card) slot.card.remove();
      gaze(slot, false, slot.last);
      return 1;
    }
    function clearAll() {
      var n = 0;
      Array.from(slots.keys()).forEach(function (id) { n += clearSlot(id); });
      return n;
    }

    // ---- window hooks (scroll, resize, ESC, target click, navigation) -------
    function onKey(e) {
      if (e.key !== "Escape" || e.isComposing || !slots.size) return;
      var ids = Array.from(slots.keys());
      clearSlot(ids[ids.length - 1]);
      e.preventDefault();
      e.stopImmediatePropagation();
      syncHooks();
    }
    function onClick(e) {
      var t = e.target;
      if (!t || t.nodeType !== 1) return;
      slots.forEach(function (slot) {
        if (slot.el && slot.last && slot.last.visible && (slot.el === t || slot.el.contains(t))) emit("highlight_clicked", { highlight_id: slot.id });
      });
    }
    function hookWindow(w) {
      if (winHooks.has(w)) return;
      var onPageHide = function () {
        slots.forEach(function (slot) { if (slot.win === w) clearSlot(slot.id); });
        unhookWindow(w);
      };
      try {
        w.addEventListener("scroll", schedule, true);
        w.addEventListener("resize", schedule);
        w.addEventListener("keydown", onKey, true);
        w.addEventListener("click", onClick, true);
        if (w !== window) w.addEventListener("pagehide", onPageHide);
      } catch (_) { return; }
      winHooks.set(w, {
        off: function () {
          try {
            w.removeEventListener("scroll", schedule, true);
            w.removeEventListener("resize", schedule);
            w.removeEventListener("keydown", onKey, true);
            w.removeEventListener("click", onClick, true);
            w.removeEventListener("pagehide", onPageHide);
          } catch (_) {}
        }
      });
    }
    function unhookWindow(w) {
      var h = winHooks.get(w);
      if (!h) return;
      winHooks.delete(w);
      h.off();
    }
    // syncHooks keeps window listeners on exactly the shell, the active frame,
    // the settings frame, and every target window while any slot exists.
    function syncHooks() {
      var want = new Set();
      if (slots.size) {
        scopes().forEach(function (sc) { if (sc.win) want.add(sc.win); });
        slots.forEach(function (slot) { if (slot.win) want.add(slot.win); });
      }
      want.forEach(hookWindow);
      Array.from(winHooks.keys()).forEach(function (w) { if (!want.has(w)) unhookWindow(w); });
      if (!slots.size && layer && !chip) { layer.remove(); layer = null; }
    }

    // ---- bridge op ------------------------------------------------------------
    function highlight(params) {
      var action = String(params.action || "");
      if (action === "clear_all") {
        var n = clearAll();
        syncHooks();
        return { payload: { cleared: n, visible: false } };
      }
      var id = String(params.id || "default");
      if (action === "clear") {
        var c = clearSlot(id);
        syncHooks();
        return { payload: { cleared: c, visible: false } };
      }
      if (action !== "show") return { error: "invalid_request:unknown_action" };
      var target = params.target && typeof params.target === "object" ? params.target : null;
      if (!target) return { error: "invalid_request:missing_target" };
      if (!slots.has(id) && slots.size >= DG_MAX_SLOTS) return { error: "too_many_highlights" };
      var found = resolve(target, "");
      if (found.error) return { error: found.error };
      var slot = slots.get(id);
      if (slot) slots.delete(id); // re-insert so the newest show is last (ESC order)
      else {
        slot = { id: id, last: null };
        buildSlotNodes(slot);
      }
      slots.set(id, slot);
      slot.style = String(params.style || "spotlight");
      slot.comment = String(params.comment || "");
      slot.target = { kind: String(target.kind || ""), ref: target.ref, generation: target.generation, selector: target.selector, text: target.text };
      slot.last = null;
      bindTarget(slot, found.el, found.sc);
      placeLayer();
      applySlotLook(slot);
      try { if (typeof found.el.scrollIntoView === "function") found.el.scrollIntoView({ block: "nearest", inline: "nearest" }); } catch (_) {}
      syncHooks();
      var p = update(slot);
      return { payload: { scope: slot.scope, visible: p.visible, rect: { x: p.x, y: p.y, width: p.width, height: p.height } } };
    }

    // ---- monitor: stop chip and structural hooks ----------------------------
    function sendFrame(obj) {
      if (!mainStream || typeof mainStream.send !== "function") return false;
      obj.tab_id = daemonTabId;
      // The stream answers false while its socket is not open (or the frame
      // is oversized); report that so callers keep their state retryable.
      try { return mainStream.send(obj) === true; } catch (_) { return false; }
    }
    function emit(name, fields) {
      if (!monitor.active || !monitor.events[name]) return;
      pending[name] = fields || {};
      if (pendingTimers[name]) clearTimeout(pendingTimers[name]);
      pendingTimers[name] = setTimeout(function () {
        var f = pending[name] || {};
        delete pending[name];
        delete pendingTimers[name];
        if (!monitor.active || !monitor.events[name]) return;
        sendFrame({
          event: "guide-monitor-event",
          monitor_event: name,
          route: clampStr(location.pathname || "/", DG_ROUTE_MAX),
          section: clampStr(f.section, DG_FIELD_MAX),
          modal: clampStr(f.modal, DG_FIELD_MAX),
          highlight_id: clampStr(f.highlight_id, DG_FIELD_MAX)
        });
      }, DG_EVENT_DEBOUNCE_MS);
    }

    function renderChip() {
      var live = monitor.ids.filter(function (id) { return !monitor.stopped[id]; });
      if (!live.length) {
        if (chip) { chip.remove(); chip = null; }
        if (!slots.size && layer) { layer.remove(); layer = null; }
        return;
      }
      if (!chip) {
        chip = doc.createElement("button");
        chip.type = "button";
        chip.className = "dg-chip";
        var dot = doc.createElement("span");
        dot.className = "dg-chip-dot";
        dot.setAttribute("aria-hidden", "true");
        var label = doc.createElement("span");
        label.className = "dg-chip-label";
        chip.appendChild(dot);
        chip.appendChild(label);
        chip.addEventListener("click", function () {
          // Hide optimistically only for stops that actually went out; an
          // unsent stop leaves the chip up so the user can click again.
          monitor.ids.forEach(function (id) {
            if (monitor.stopped[id]) return;
            if (sendFrame({ event: "guide-monitor-stop", monitor_id: id })) monitor.stopped[id] = true;
          });
          renderChip();
        });
        ensureLayer().appendChild(chip);
        placeLayer();
      }
      chip.lastChild.textContent = wsT("js.dc.guide.monitor_stop");
    }

    function applyMonitor(state) {
      var mons = state && state.active && Array.isArray(state.monitors) ? state.monitors : [];
      var ids = [], events = {}, maxMs = 0, stopped = {};
      mons.forEach(function (m) {
        if (!m || typeof m.monitor_id !== "string" || !m.monitor_id) return;
        ids.push(m.monitor_id);
        if (monitor.stopped[m.monitor_id]) stopped[m.monitor_id] = true;
        (Array.isArray(m.events) ? m.events : []).forEach(function (n) { if (DG_MONITOR_EVENTS[n]) events[n] = 1; });
        maxMs = Math.max(maxMs, Number(m.expires_in_ms) || 0);
      });
      monitor.ids = ids;
      monitor.stopped = stopped;
      monitor.active = ids.some(function (id) { return !stopped[id]; });
      monitor.events = monitor.active ? events : {};
      if (monitor.timer) { clearTimeout(monitor.timer); monitor.timer = 0; }
      // Failsafe: hide the chip shortly after the longest TTL even if the
      // expiry push is lost.
      if (monitor.active && maxMs > 0) monitor.timer = setTimeout(function () { applyMonitor(null); }, maxMs + 2000);
      if (!monitor.active) {
        Object.keys(pendingTimers).forEach(function (k) { clearTimeout(pendingTimers[k]); });
        pending = {};
        pendingTimers = {};
      }
      renderChip();
      syncMonitorHooks();
    }

    function dialogName(el) {
      if (el === shSettingsDialog) return "settings";
      return clampStr(el.id || "dialog", DG_FIELD_MAX);
    }
    function onModalRecord(hook, el) {
      if (!el || el.tagName !== "DIALOG" || dcIsExcluded(el)) return;
      var open = !!el.open;
      var key = "dlg";
      if (!hook.last[key]) hook.last[key] = new WeakMap();
      if (hook.last[key].get(el) === open) return;
      hook.last[key].set(el, open);
      emit(open ? "modal_opened" : "modal_closed", { modal: dialogName(el) });
    }
    function watchDoc(d, kind) {
      var w = d && d.defaultView;
      var MO = (w && w.MutationObserver) || window.MutationObserver;
      if (!MO || !d.documentElement) return;
      var filter = ["open"];
      if (kind === "frame") filter.push("data-tab", "data-ctab");
      if (kind === "settings") filter.push("aria-current");
      var hook = { doc: d, kind: kind, last: {}, mo: null };
      if (kind === "frame") {
        var g = d.getElementById("grid");
        hook.last.tab = g ? g.getAttribute("data-tab") || "" : "";
        hook.last.ctab = g ? g.getAttribute("data-ctab") || "" : "";
      }
      if (kind === "settings") {
        var cur = d.querySelector('[data-settings-section][aria-current="true"]');
        hook.last.section = cur ? cur.getAttribute("data-settings-section") || "" : "";
      }
      d.querySelectorAll("dialog").forEach(function (dl) {
        if (!hook.last.dlg) hook.last.dlg = new WeakMap();
        hook.last.dlg.set(dl, !!dl.open);
      });
      hook.mo = new MO(function (recs) {
        for (var i = 0; i < recs.length; i++) {
          var t = recs[i].target, a = recs[i].attributeName;
          if (a === "open") onModalRecord(hook, t);
          else if ((a === "data-tab" || a === "data-ctab") && t.id === "grid") {
            var v = t.getAttribute(a) || "", k = a === "data-tab" ? "tab" : "ctab";
            if (v !== hook.last[k]) { hook.last[k] = v; emit("tab_switched", { section: v }); }
          } else if (a === "aria-current" && t.getAttribute("aria-current") === "true" && t.hasAttribute("data-settings-section")) {
            var sec = t.getAttribute("data-settings-section") || "";
            if (sec !== hook.last.section) { hook.last.section = sec; emit("section_changed", { section: sec }); }
          }
        }
      });
      hook.mo.observe(d.documentElement, { attributes: true, subtree: true, attributeFilter: filter });
      monHooks.push(hook);
    }
    function wantsHooks() {
      var e = monitor.events;
      return monitor.active && !!(e.tab_switched || e.section_changed || e.modal_opened || e.modal_closed);
    }
    // syncMonitorHooks re-attaches observers to the shell, the active frame,
    // and the settings frame. Runs on a monitor push, a shell route change, an
    // iframe load, and the settings dialog opening or closing.
    function syncMonitorHooks() {
      var targets = [];
      if (wantsHooks()) {
        targets.push({ doc: doc, kind: "shell" });
        var fd = shOn && entryDoc(shOn);
        if (fd) targets.push({ doc: fd, kind: "frame" });
        var sd = shSettingsEntry && entryDoc(shSettingsEntry);
        if (sd) targets.push({ doc: sd, kind: "settings" });
      }
      monHooks = monHooks.filter(function (h) {
        var keep = targets.some(function (t) { return t.doc === h.doc && t.kind === h.kind; });
        if (!keep) { try { h.mo.disconnect(); } catch (_) {} }
        return keep;
      });
      targets.forEach(function (t) {
        if (!monHooks.some(function (h) { return h.doc === t.doc; })) {
          try { watchDoc(t.doc, t.kind); } catch (_) {}
        }
      });
    }

    // ---- wiring ----------------------------------------------------------------
    function onLayout() {
      placeLayer();
      syncMonitorHooks();
      if (!slots.size) return;
      syncHooks();
      schedule();
    }
    doc.addEventListener("ws-daemon-guide-monitor", function (e) { applyMonitor(e && e.detail); });
    doc.addEventListener("ws-shell-route", onLayout);
    // An iframe load does not bubble; the capture phase still reaches doc.
    doc.addEventListener("load", function (e) {
      if (e.target && e.target.tagName === "IFRAME") onLayout();
    }, true);
    doc.addEventListener("ws-page-visibility", function () { if (slots.size) schedule(); });
    // shSettingsDialog is assigned by a later fragment; read the element here.
    var settingsDialogEl = typeof doc.getElementById === "function" ? doc.getElementById("workflow-settings") : null;
    if (settingsDialogEl && typeof window.MutationObserver === "function") {
      new MutationObserver(onLayout).observe(settingsDialogEl, { attributes: true, attributeFilter: ["open"] });
    }

    window.WsDaemonGuide = {
      highlight: highlight,
      stats: function () {
        return { slots: slots.size, raf_pending: !!raf, raf_count: rafCount, monitor_active: monitor.active, hooks: monHooks.length, windows: winHooks.size };
      }
    };
  })();
