  // --- new-session modal controls: combobox, named-tier pill, effort knob ---
  // nsCombo builds a searchable/editable combobox under root (.ns-cb).
  // When o.free is provided (Model), typing updates the value live and allows
  // unlisted entries; without it (Named tier), typing filters only and blur
  // restores the picked value.
  function nsCombo(root, o) {
    var inBox = root.appendChild(mk("div", "ns-cb-in"));
    if (o.prefix) inBox.appendChild(mk("span", "ns-tl", o.prefix));
    var input = inBox.appendChild(mk("input"));
    input.type = "text";
    input.autocomplete = "off";
    input.spellcheck = false;
    input.setAttribute("role", "combobox");
    input.setAttribute("aria-autocomplete", "list");
    input.setAttribute("aria-expanded", "false");
    if (o.ariaLabel) input.setAttribute("aria-label", o.ariaLabel);
    if (o.placeholder) input.placeholder = o.placeholder;
    if (o.inputAttr) input.setAttribute(o.inputAttr[0], o.inputAttr[1]);
    var tg = inBox.appendChild(rpBtn("ns-cb-tg", "▾"));
    tg.tabIndex = -1;
    tg.setAttribute("aria-hidden", "true");
    var ul = root.appendChild(mk("ul", "ns-cb-list"));
    ul.id = rpId();
    ul.setAttribute("role", "listbox");
    ul.hidden = true;
    input.setAttribute("aria-controls", ul.id);
    var open = false, filtering = false, hi = -1, items = [], listSig = "";
    function focused() {
      var d = typeof document !== "undefined" ? document : (typeof doc !== "undefined" ? doc : null);
      return !!(d && d.activeElement === input);
    }
    function renderList(force) {
      var q = filtering ? input.value.trim().toLowerCase() : "";
      var all = o.items();
      items = all.filter(function (it) {
        return !q || (it.val + " " + it.label + " " + (it.sub || "")).toLowerCase().indexOf(q) >= 0;
      });
      var cur = o.current();
      var sig = JSON.stringify([q, cur, hi, items.map(function (it) { return [it.val, it.label, it.sub || "", !!it.disabled, it.title || ""]; })]);
      if (!force && sig === listSig) return;
      listSig = sig;
      ul.textContent = "";
      if (!items.length) {
        var none = ul.appendChild(mk("li", "none", wsT("js.lead.ns.nomatch")));
        none.setAttribute("aria-disabled", "true");
        return;
      }
      items.forEach(function (it, i) {
        var cls = [i === hi ? "hi" : "", it.disabled ? "dis" : ""].filter(Boolean).join(" ");
        var li = ul.appendChild(mk("li", cls));
        li.setAttribute("role", "option");
        li.setAttribute("data-val", it.val);
        li.setAttribute("aria-selected", String(it.val === cur));
        if (it.disabled) li.setAttribute("aria-disabled", "true");
        if (it.title) li.title = it.title;
        li.appendChild(it.manual ? mk("i", null, it.label) : mk("span", null, it.label));
        if (it.sub) li.appendChild(mk("small", null, it.sub));
        li.addEventListener("mousedown", function (e) {
          if (e && e.preventDefault) e.preventDefault();
          if (!it.disabled) choose(it);
        });
      });
    }
    function placeList() {
      if (!ul.classList || !inBox.getBoundingClientRect) return;
      var modal = root.closest && root.closest(".ns-modal");
      if (!modal || !modal.getBoundingClientRect) { ul.classList.remove("ns-cb-up"); return; }
      var ir = inBox.getBoundingClientRect(), mr = modal.getBoundingClientRect();
      var below = mr.bottom - ir.bottom, above = ir.top - mr.top;
      ul.classList.toggle("ns-cb-up", below < 150 && above > below);
    }
    function show(v) {
      if (v && input.disabled) v = false;
      open = !!v;
      ul.hidden = !open;
      input.setAttribute("aria-expanded", String(open));
      if (open) { renderList(false); placeList(); }
    }
    function choose(it) {
      show(false);
      filtering = false;
      hi = -1;
      o.pick(it);
      C.sync(true);
      if (input.blur) input.blur();
    }
    function step(d) {
      if (!items.length) return;
      var n = items.length, next = hi;
      for (var k = 0; k < n; k++) {
        next = next < 0 ? (d > 0 ? 0 : n - 1) : (next + d + n) % n;
        if (!items[next].disabled) { hi = next; break; }
      }
      renderList(true);
      var h = ul.querySelector && ul.querySelector(".hi");
      if (h && h.scrollIntoView) h.scrollIntoView({ block: "nearest" });
    }
    input.addEventListener("focus", function () {
      if (input.disabled) return;
      filtering = false;
      hi = -1;
      if (input.select) input.select();
      show(true);
    });
    input.addEventListener("blur", function () {
      show(false);
      filtering = false;
      hi = -1;
      if (o.onBlur) o.onBlur(input.value);
      C.sync(true);
    });
    input.addEventListener("input", function () {
      filtering = true;
      hi = -1;
      if (o.free) o.free(input.value);
      show(true);
    });
    input.addEventListener("keydown", function (e) {
      if (e.isComposing) return;
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        if (!open) show(true);
        step(e.key === "ArrowDown" ? 1 : -1);
      } else if (e.key === "Enter") {
        e.preventDefault();
        if (open && hi >= 0 && items[hi] && !items[hi].disabled) choose(items[hi]);
        else { show(false); if (input.blur) input.blur(); }
      } else if (e.key === "Escape") {
        if (open) {
          e.preventDefault();
          if (e.stopPropagation) e.stopPropagation();
          show(false);
        }
      }
    });
    tg.addEventListener("mousedown", function (e) {
      if (e && e.preventDefault) e.preventDefault();
      if (input.disabled) return;
      if (open) show(false);
      else if (input.focus) input.focus();
    });
    var C = {
      input: input,
      list: ul,
      inBox: inBox,
      isOpen: function () { return open; },
      close: function () {
        if (!open) return false;
        show(false);
        return true;
      },
      setDisabled: function (dis) {
        var off = !!dis;
        input.disabled = tg.disabled = off;
        if (inBox.toggleAttribute) inBox.toggleAttribute("data-off", off);
        else if (off) inBox.setAttribute("data-off", "");
        else if (inBox.removeAttribute) inBox.removeAttribute("data-off");
        if (off && open) show(false);
      },
      sync: function (force) {
        if (force || !focused()) {
          var v = o.display ? o.display(o.current()) : o.current();
          if (input.value !== v) input.value = v;
        }
        renderList(false);
      }
    };
    return C;
  }

  // --- named-tier picker (new-session modal header pill) -------------------
  // Inline searchable combobox for agent.tier_routing named bundles from
  // /support/choices (supChoices.tiers). Picking one calls fill(tier),
  // which fills harness, model, and effort; launch sends those concrete fields,
  // and permission + plugin isolation stay the user's own. manual() returns to
  // manual selection when the user edits any of the three fields.
  function leadTierPicker(box, usable, fill) {
    var T = { name: "", busy: false, note: "" };
    function tiers() { return supChoices && Array.isArray(supChoices.tiers) ? supChoices.tiers : []; }
    function find(name) { return tiers().filter(function (t) { return t.name === name; })[0]; }
    function shown(t) { return [t.harness, t.provider, t.model, t.effort].filter(Boolean).join(" · "); }
    function why(t) { return t.error || (usable(t) ? "" : wsT("js.lead.tier.off", t.harness || "")); }
    var combo = nsCombo(box, {
      prefix: wsT("js.lead.ns.tier.short"),
      ariaLabel: wsT("js.lead.tier"),
      placeholder: wsT("js.lead.tier.manual"),
      inputAttr: ["data-tier-picker", "1"],
      items: function () {
        var a = [{ val: "", label: wsT("js.lead.tier.manual"), manual: true }];
        tiers().forEach(function (t) {
          var reason = why(t);
          a.push({ val: t.name, label: t.name, sub: reason || shown(t), disabled: !!reason, title: reason || t.trace || "" });
        });
        return a;
      },
      current: function () { return T.name; },
      pick: function (it) { T.pick(it.val); },
      onBlur: function () { T.draw(); }
    });
    T.input = combo.input;
    T.list = combo.list;
    T.close = combo.close;
    T.pick = function (name) {
      var t = name && find(name);
      if (!t || why(t)) { T.name = ""; T.draw(); return; }
      T.name = t.name;
      fill(t);
      T.draw();
    };
    T.draw = function () {
      var list = tiers();
      var cur = T.name && find(T.name);
      if (!cur || why(cur)) T.name = "";
      cur = T.name && find(T.name);
      box.hidden = !list.length;
      combo.setDisabled(T.busy || !list.length);
      if (combo.input.classList) combo.input.classList.toggle("set", !!T.name);
      T.note = cur ? wsT("js.lead.tier.picked", cur.name, shown(cur)) : wsT("js.lead.tier.hint");
      box.title = T.note;
      combo.sync(false);
    };
    T.manual = function () {
      if (!T.name) return;
      T.name = "";
      T.draw();
    };
    T.reset = function () { T.name = ""; T.draw(); };
    T.current = function () {
      var t = T.name && find(T.name);
      return t && !why(t) ? t : null;
    };
    return T;
  }

  // --- horizontal effort knob (new-session modal) --------------------------
  // Reuses tracker chat's .knob/.ktrack/.kdot/.kthumb + supKnob pointer
  // capture and snap-to-nearest mechanics, plus Arrow/Home/End keyboard steps.
  function nsEffortKnob(el, onPick, onPreview) {
    el.setAttribute("role", "slider");
    el.setAttribute("data-effort-knob", "1");
    el.setAttribute("aria-label", wsT("js.sup.effort"));
    var track = el.appendChild(mk("div", "ktrack"));
    var K = { levels: [], value: "", disabled: false };
    var drag = null;
    function pct(i, n) { return n < 2 ? 50 : i / (n - 1) * 100; }
    function idxAt(x) {
      var r = track.getBoundingClientRect ? track.getBoundingClientRect() : { left: 0, width: 100 };
      return Math.round(Math.max(0, Math.min(1, (x - r.left) / (r.width || 1))) * (K.levels.length - 1));
    }
    function render(pv) {
      var lv = K.levels, off = K.disabled || !lv.length;
      var at = pv != null ? pv : lv.indexOf(K.value);
      if (el.toggleAttribute) el.toggleAttribute("data-off", off);
      else if (off) el.setAttribute("data-off", "");
      else if (el.removeAttribute) el.removeAttribute("data-off");
      el.setAttribute("aria-disabled", String(off));
      el.tabIndex = off ? -1 : 0;
      el.setAttribute("aria-valuemin", "0");
      el.setAttribute("aria-valuemax", String(Math.max(0, lv.length - 1)));
      el.setAttribute("aria-valuenow", String(Math.max(at, 0)));
      el.setAttribute("aria-valuetext", at >= 0 && lv[at] ? lv[at] : wsT("js.sup.default"));
      el.title = off ? "" : wsT("js.lead.ns.effort.tip", lv.join(" · "));
      track.textContent = "";
      if (!off) {
        lv.forEach(function (l, i) {
          var d = track.appendChild(mk("span", "kdot" + (i === at ? " on" : "")));
          if (d.style) d.style.left = pct(i, lv.length) + "%";
          d.setAttribute("data-effort", l);
          d.title = l;
        });
        if (at >= 0) {
          var th = track.appendChild(mk("span", "kthumb"));
          if (th.style) th.style.left = pct(at, lv.length) + "%";
        }
      }
      if (onPreview) onPreview(off ? "" : (at >= 0 && lv[at] ? lv[at] : ""), off);
    }
    function choose(i) {
      if (i < 0 || i >= K.levels.length) return;
      var v = K.levels[i];
      K.value = v;
      render();
      onPick(v);
    }
    el.addEventListener("pointerdown", function (e) {
      if ((e && e.button) || K.disabled || !K.levels.length) return;
      drag = { i: idxAt(e.clientX) };
      if (el.setPointerCapture && e.pointerId != null) { try { el.setPointerCapture(e.pointerId); } catch (_) {} }
      render(drag.i);
      if (e.preventDefault) e.preventDefault();
      if (el.focus) el.focus({ preventScroll: true });
    });
    el.addEventListener("pointermove", function (e) {
      if (!drag) return;
      var i = idxAt(e.clientX);
      if (i !== drag.i) { drag.i = i; render(i); }
    });
    el.addEventListener("pointerup", function () {
      if (!drag) return;
      var i = drag.i;
      drag = null;
      choose(i);
    });
    el.addEventListener("pointercancel", function () {
      if (!drag) return;
      drag = null;
      render();
    });
    el.addEventListener("keydown", function (e) {
      if (K.disabled || !K.levels.length) return;
      var n = K.levels.length, cur = K.levels.indexOf(K.value), i = cur;
      if (e.key === "ArrowRight" || e.key === "ArrowUp") i = cur < 0 ? 0 : Math.min(n - 1, cur + 1);
      else if (e.key === "ArrowLeft" || e.key === "ArrowDown") i = cur < 0 ? 0 : Math.max(0, cur - 1);
      else if (e.key === "Home") i = 0;
      else if (e.key === "End") i = n - 1;
      else if (e.key === "Backspace" || e.key === "Delete") {
        e.preventDefault();
        if (K.value) { K.value = ""; render(); onPick(""); }
        return;
      } else return;
      e.preventDefault();
      if (i !== cur) choose(i);
    });
    K.set = function (levels, value, disabled) {
      var list = (levels || []).slice();
      if (value && list.indexOf(value) < 0) list.push(value);
      K.levels = list;
      K.value = value || "";
      K.disabled = !!disabled || !list.length;
      if (drag && K.disabled) drag = null;
      render();
    };
    return K;
  }
