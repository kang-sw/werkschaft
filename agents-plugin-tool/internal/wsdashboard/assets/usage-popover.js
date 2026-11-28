    /* ---- Popover header, Group 1/2 rows, collapsible sessions drawer, actions, and lifecycle ---- */
    var CHEV_PATH = ["M6 9l6 6 6-6"];
    var CHECK_PATH = ["M5 12.5l4.5 4.5L19 7.5"];
    var WARN_PATH = ["M12 8v5", "M12 16.5v.5"];
    var REFRESH_PATH = ["M21 12a9 9 0 1 1-2.64-6.36", "M21 3v6h-6"];
    var HELP_PATH = ["M12 22a10 10 0 1 0 0-20 10 10 0 0 0 0 20z", "M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3", "M12 17h.01"];

    function drawSync(now) {
      if (!popover) return;
      var node = popover.querySelector(".up-sync"), txtEl = popover.querySelector(".up-sync-t");
      if (!node || !txtEl) return;
      var tone = "ok", txt = t("syncOk"), staleMax = 0;
      (data.entries || []).forEach(function (e) {
        active(e, now).forEach(function (w) {
          var at = observation(w, e);
          if (stale(at, now)) staleMax = Math.max(staleMax, Math.round((now - at) / 60000));
        });
      });
      if (refreshing) { tone = "busy"; txt = t("syncBusy"); }
      else if (loadError) { tone = "err"; txt = t("syncErr"); }
      else if (staleMax > 0) { tone = "warn"; txt = t("syncStale", staleMax); }
      node.setAttribute("data-tone", tone);
      txtEl.textContent = txt;
    }
    function renderLegend() {
      if (!popover) return;
      var L = popover.querySelector(".up-legend");
      if (!L) return;
      L.textContent = "";
      function item(inner, heading, desc) {
        var wrap = el("span"), v = svgNode("svg", { viewBox: "0 0 34 14", "aria-hidden": "true" });
        inner(v); wrap.appendChild(v);
        var txt = el("span");
        txt.appendChild(el("b", "", heading + " "));
        txt.appendChild(doc.createTextNode ? doc.createTextNode(desc) : el("span", "", desc));
        wrap.appendChild(txt); L.appendChild(wrap);
      }
      var ln = function (v, a) { v.appendChild(svgNode("path", Object.assign({ "class": "up-ln", "stroke-width": 1.2 }, a))); };
      if (mode === "progress") {
        item(function (v) { ln(v, { d: "M2 12L32 2", "stroke-dasharray": "4 4" }); }, t("ideal"), "");
        item(function (v) { v.appendChild(svgNode("polygon", { points: "2,12 32,2 2,2", fill: "var(--wb-fail)", opacity: "0.22" })); }, "", t("legendExcess"));
        item(function (v) { v.appendChild(svgNode("rect", { x: 4, y: 1, width: 26, height: 12, rx: 3, fill: "var(--k-claude)" })); }, "", t("legendPin"));
        item(function (v) {
          v.appendChild(svgNode("path", { d: "M2 9H12", stroke: "var(--wb-muted-fg)", "stroke-width": 2, fill: "none" }));
          v.appendChild(svgNode("path", { d: "M12 9L26 5", stroke: "var(--wb-muted-fg)", "stroke-width": 2, fill: "none", "stroke-dasharray": "4 3" }));
        }, t("estimated"), "");
        item(function (v) {
          v.appendChild(svgNode("circle", { cx: 17, cy: 7, r: 5.2, fill: "none", stroke: "var(--wb-warn-ink)", "stroke-width": 1.4 }));
          v.appendChild(svgNode("path", { d: "M17 4.5V7l1.8 1.1", fill: "none", stroke: "var(--wb-warn-ink)", "stroke-width": 1.4, "stroke-linecap": "round" }));
        }, "", t("legendClock"));
        item(function (v) { v.appendChild(svgNode("circle", { cx: 17, cy: 7, r: 4, fill: "var(--k-codex)" })); }, "", t("legendFocus"));
      } else {
        item(function (v) { ln(v, { d: "M17 1V13", "stroke-dasharray": "2 3" }); }, t("now"), t("nowTitle"));
        item(function (v) {
          v.appendChild(svgNode("rect", { x: 2, y: 3, width: 30, height: 8, rx: 4, fill: "var(--k-claude)", opacity: "0.25" }));
          v.appendChild(svgNode("rect", { x: 2, y: 3, width: 14, height: 8, rx: 4, fill: "var(--k-claude)" }));
        }, "", t("legendGantt"));
        item(function (v) {
          v.appendChild(svgNode("rect", { x: 2, y: 3, width: 30, height: 8, rx: 4, fill: "var(--k-claude)", opacity: "0.25" }));
          v.appendChild(svgNode("rect", { x: 2, y: 3, width: 10, height: 8, fill: "var(--k-claude)" }));
          v.appendChild(svgNode("rect", { x: 12, y: 3, width: 12, height: 8, fill: "var(--wb-fail)" }));
        }, "", t("legendGanttOver"));
        item(function (v) { v.appendChild(svgNode("text", { x: 2, y: 10, fill: "var(--wb-muted-fg)", "class": "up-lg-txt" }, "\u21bb 42m")); }, "", t("legendReset"));
        item(function (v) { ln(v, { d: "M2 12L32 2", "stroke-dasharray": "4 4" }); }, t("ideal"), "");
        item(function (v) { v.appendChild(svgNode("path", { d: "M2 9L30 4", stroke: "var(--k-claude)", "stroke-width": 2, fill: "none", "stroke-dasharray": "4 3" })); }, "", t("legendPreview"));
      }
    }
    function buildNumCell(s, kind, now, extraCls, extraAttrs) {
      var cls = "up-nc" + (extraCls ? " " + extraCls : "") + (s && s.old ? " is-stale" : "") + (s && s.disabled ? " usage-disabled" : "");
      var cell = el("div", cls);
      cell.setAttribute("data-kind", kind);
      Object.keys(extraAttrs || {}).forEach(function (k) { cell.setAttribute(k, extraAttrs[k]); });
      if (!s) {
        var emptyTop = el("div", "up-nc-t");
        emptyTop.appendChild(el("span", "up-nc-k", kind === "five_hour" ? "5H" : "7D"));
        emptyTop.appendChild(el("span", "up-nc-n dim", "—"));
        cell.appendChild(emptyTop);
        return cell;
      }
      var top = el("div", "up-nc-t");
      top.appendChild(el("span", "up-nc-k", kind === "five_hour" ? "5H" : "7D"));
      top.appendChild(el("span", "up-nc-n", s.noRecent && !s.isPreview && !s.at ? "—" : Math.round(s.pct) + "%"));
      if (s.delta !== null) {
        top.appendChild(el("span", "up-nc-p " + (s.delta > 0 ? "over" : s.delta < 0 ? "under" : ""), signedDelta(s.delta)));
      }
      if (s.disabled) top.appendChild(el("span", "up-pill sm", t("disabled")));
      if (s.isPreview) {
        top.appendChild(el("span", "up-nc-age", previewAgeText(s.at, now)));
      } else if (s.old) {
        var ck = clock("up-nc-clk");
        ck.appendChild(svgNode("title", {}, t("staleMin", Math.max(1, Math.round((now - s.at) / 60000)))));
        top.appendChild(ck);
      }
      var rs = el("span", "up-nc-r", "\u21bb " + shortResetText(s.w, now));
      rs.title = t("resets", s.knownReset ? new Date(s.reset).toLocaleString(locale()) : t("unknown"));
      top.appendChild(rs);
      cell.appendChild(top);
      var mg = el("div", "up-mg");
      setVar(mg, "--kc", "var(--k-" + s.def.k + ")");
      var elPct = s.elapsedNow !== null ? s.elapsedNow * 100 : 0;
      var p = Math.max(0, Math.min(100, s.pct));
      var f1 = el("i", "f1");
      f1.style.width = (s.knownReset ? Math.min(p, elPct) : p).toFixed(1) + "%";
      mg.appendChild(f1);
      if (s.knownReset && p > elPct) {
        var f2 = el("i", "f2");
        f2.style.left = elPct.toFixed(1) + "%";
        f2.style.width = (p - elPct).toFixed(1) + "%";
        mg.appendChild(f2);
      }
      if (s.knownReset) {
        var nw = el("i", "nw");
        nw.style.left = elPct.toFixed(1) + "%";
        mg.appendChild(nw);
      }
      cell.appendChild(mg);
      return cell;
    }
    function buildSlotPills(harness) {
      var list = slotsOf(harness);
      if (list.length < 2) return null;
      var wrap = el("span", "up-slots");
      wrap.setAttribute("role", "group");
      wrap.setAttribute("aria-label", harness);
      list.forEach(function (slot) {
        var lbl = slot.id === "default" ? t("defaultSlot") : (slot.label || slot.id);
        var isBusy = busyHarness === harness && busySlotId === slot.id;
        var b = el("button", "up-sp" + (isBusy ? " busy" : ""), lbl);
        b.type = "button";
        b.setAttribute("data-slot-harness", harness);
        b.setAttribute("data-slot-id", slot.id);
        b.setAttribute("aria-pressed", String(!!slot.active));
        b.disabled = switchBusy;
        b.title = slot.active ? t("slotActiveTip", lbl) : t("slotSwitchTip", lbl);
        b.addEventListener("click", function (ev) {
          if (ev && ev.stopPropagation) ev.stopPropagation();
          if (slot.active || switchBusy) return;
          setPreview(null, null);
          switchHarnessSlot(harness, slot.id, lbl);
        });
        var hDef = defForHarness(harness, "");
        if (!slot.active && !(hDef && hDef.noQuota)) {
          b.addEventListener("mouseenter", function () { setPreview(harness, slot.id); });
          b.addEventListener("mouseleave", function () { setPreview(null, null); });
          b.addEventListener("focus", function () { setPreview(harness, slot.id); });
          b.addEventListener("blur", function () { setPreview(null, null); });
        }
        wrap.appendChild(b);
      });
      return wrap;
    }
    function buildAuthCta() {
      var a = el("a", "up-pill fail sm", t("signInRequired"));
      a.href = "/settings?section=account";
      if (openSettings) {
        a.addEventListener("click", function (e) {
          if (e && (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || e.button)) return;
          if (e && e.preventDefault) e.preventDefault();
          if (e && e.stopPropagation) e.stopPropagation();
          openSettings("/settings?section=account", a);
        });
      }
      return a;
    }
    function buildRowBanner(harness) {
      var b = banners[harness];
      if (!b || !b.text) return null;
      var n = el("div", "up-rbanner" + (b.tone ? " " + b.tone : ""));
      n.setAttribute("role", "status");
      n.appendChild(svgIcon(b.tone === "warn" || b.tone === "fail" ? WARN_PATH : CHECK_PATH, "up-ic"));
      n.appendChild(el("span", "", b.text));
      return n;
    }
    function buildProviderRow(def, now, withSlots) {
      var e = entryOf(def);
      var slots = withSlots && switcherOn ? slotsOf(def.harness) : [];
      if (!e && !slots.length) return null;
      var r = el("div", "up-hr");
      r.setAttribute("data-k", def.k);
      var rec = { k: def.k, el: r, liveCells: [], liveStatus: [], prevCells: [], nocacheEls: [] };
      var id = el("div", "up-hr-id");
      id.appendChild(hbadge(def, false));
      var displayName = def.harness === "codex" && e && e.harness === "codex_openai" ? localizedEntryName(e)
        : (def.group && e && e.label ? e.label : def.name);
      id.appendChild(el("span", "up-hr-name", displayName));
      if (withSlots && switcherOn) {
        var pills = buildSlotPills(def.harness);
        if (pills) id.appendChild(pills);
      }
      r.appendChild(id);
      var s5 = activeSeries(def, "five_hour", now);
      var s7 = activeSeries(def, "seven_day", now);
      if (e && e.pending) {
        var pendEl = el("div", "up-hr-status up-hr-status-live");
        pendEl.setAttribute("aria-busy", "true");
        var sp = el("span", "spin"); sp.setAttribute("aria-hidden", "true");
        pendEl.appendChild(sp);
        pendEl.appendChild(el("span", "", t("pending")));
        r.appendChild(pendEl);
        rec.liveStatus.push(pendEl);
      } else if (!s5 && !s7) {
        var st = el("div", "up-hr-status up-hr-status-live");
        st.appendChild(el("span", "", e ? statusText(e.status) : t("unavailable")));
        if (e && e.status === "sign-in required" && !def.group) st.appendChild(buildAuthCta());
        r.appendChild(st);
        rec.liveStatus.push(st);
      } else {
        var l5 = buildNumCell(s5, "five_hour", now, "up-nc-live", null);
        var l7 = buildNumCell(s7, "seven_day", now, "up-nc-live", null);
        r.appendChild(l5);
        r.appendChild(l7);
        rec.liveCells.push(l5, l7);
      }
      // Pre-built inactive slot preview cells (hidden until pill hover/focus).
      if (withSlots && switcherOn && slots.length > 1) {
        slots.forEach(function (slot) {
          if (slot.active) return;
          var p5 = previewSeries(def, slot.id, "five_hour", now);
          var p7 = previewSeries(def, slot.id, "seven_day", now);
          if (p5 || p7) {
            var c5 = buildNumCell(p5, "five_hour", now, "up-nc-prev", { "data-preview-slot": slot.id });
            var c7 = buildNumCell(p7, "seven_day", now, "up-nc-prev", { "data-preview-slot": slot.id });
            c5.hidden = true; c7.hidden = true;
            r.appendChild(c5); r.appendChild(c7);
            rec.prevCells.push({ slot: slot.id, el: c5 }, { slot: slot.id, el: c7 });
          } else {
            var nc = el("div", "up-hr-status up-hr-nocache", t("previewNoCache"));
            nc.setAttribute("data-preview-slot", slot.id);
            nc.hidden = true;
            r.appendChild(nc);
            rec.nocacheEls.push({ slot: slot.id, el: nc });
          }
        });
      }
      if (rowCache) rowCache.push(rec);
      return r;
    }
    function eligibleConversations() {
      if (!accountData || !Array.isArray(accountData.conversations)) return [];
      return accountData.conversations.filter(function (c) {
        var list = slotsOf(c.backend);
        return list.length > 1 || !!c.pending_account_slot;
      });
    }
    function drawSessions() {
      if (!popover) return;
      var box = popover.querySelector(".up-sess");
      if (!box) return;
      box.textContent = "";
      if (!switcherOn || !accountData) { box.hidden = true; return; }
      var list = eligibleConversations();
      var hasMultiSlots = Object.keys((accountData && accountData.slots) || {}).some(function (h) { return slotsOf(h).length > 1; });
      if (!hasMultiSlots && !list.length) { box.hidden = true; return; }
      box.hidden = false;
      var pendCount = list.filter(function (c) { return !!c.pending_account_slot; }).length;
      var hd = el("button", "up-sess-hd");
      hd.type = "button";
      hd.setAttribute("aria-expanded", String(sessOpen));
      hd.appendChild(svgIcon(CHEV_PATH, "up-ic"));
      hd.appendChild(el("span", "", t("activeSessions") + " (" + list.length + ")"));
      if (pendCount > 0) hd.appendChild(el("span", "up-pill warn sm", t("sessQueuedBadge", pendCount)));
      hd.appendChild(el("span", "up-rsp"));
      hd.appendChild(el("span", "up-sub", t(sessOpen ? "sessCollapse" : "sessExpand")));
      hd.addEventListener("click", function (ev) {
        if (ev && ev.stopPropagation) ev.stopPropagation();
        sessOpen = !sessOpen;
        drawSessions();
        place();
      });
      box.appendChild(hd);
      if (!sessOpen) return;
      var body = el("div", "up-sess-body");
      if (!list.length) {
        body.appendChild(el("div", "up-sess-empty", t("sessEmpty")));
      }
      list.forEach(function (conv) {
        var row = el("div", "up-srow");
        row.setAttribute("data-conv-id", conv.id);
        var top = el("div", "up-srow-t");
        var bDef = defForHarness(conv.backend, "");
        if (bDef) top.appendChild(hbadge(bDef, true));
        var titleText = conv.label || conv.id;
        var ttl = el("span", "up-ttl", titleText);
        ttl.title = titleText;
        top.appendChild(ttl);
        top.appendChild(el("span", "up-pill sm", conv.backend));
        row.appendChild(top);
        var slotsWrap = el("div", "up-srow-slots");
        var curSlot = conv.account_slot_id || "default";
        slotsOf(conv.backend).forEach(function (slot) {
          var isCur = slot.id === curSlot, isQueued = conv.pending_account_slot === slot.id;
          var lbl = slot.id === "default" ? t("defaultSlot") : (slot.label || slot.id);
          var sb = el("button", "up-sp" + (isQueued ? " queued" : ""), lbl);
          sb.type = "button";
          sb.setAttribute("data-slot-id", slot.id);
          sb.setAttribute("aria-pressed", String(isCur));
          sb.disabled = switchBusy;
          if (isQueued) {
            var qTag = el("span", "up-qtag", t("queuedShort"));
            sb.appendChild(qTag);
          }
          sb.addEventListener("click", function (ev) {
            if (ev && ev.stopPropagation) ev.stopPropagation();
            if (isCur || switchBusy) return;
            confirmConv = { convId: conv.id, backend: conv.backend, slotId: slot.id, slotLabel: lbl, convTitle: titleText };
            drawSessions();
            place();
          });
          slotsWrap.appendChild(sb);
        });
        if (conv.pending_account_slot) {
          slotsWrap.appendChild(el("span", "up-pend", "\u2192 " + t("queuedSlot", slotDisplayLabel(conv.backend, conv.pending_account_slot))));
          var quickNow = el("button", "up-cbtn primary", t("switchNow"));
          quickNow.type = "button";
          quickNow.setAttribute("data-mode", "now");
          quickNow.disabled = switchBusy;
          quickNow.addEventListener("click", function (ev) {
            if (ev && ev.stopPropagation) ev.stopPropagation();
            switchSessionSlot(conv, conv.pending_account_slot, "now");
          });
          slotsWrap.appendChild(quickNow);
        }
        row.appendChild(slotsWrap);
        if (confirmConv && confirmConv.convId === conv.id) {
          var cf = el("div", "up-confirm");
          cf.setAttribute("role", "group");
          cf.appendChild(el("span", "", t("switchConfirm", confirmConv.slotLabel, confirmConv.convTitle)));
          [[t("switchNow"), "now", "primary"], [t("switchBoundary"), "queue", ""]].forEach(function (cfg) {
            var cb = el("button", "up-cbtn" + (cfg[2] ? " " + cfg[2] : ""), cfg[0]);
            cb.type = "button";
            cb.setAttribute("data-mode", cfg[1]);
            cb.disabled = switchBusy;
            cb.addEventListener("click", function (ev) {
              if (ev && ev.stopPropagation) ev.stopPropagation();
              switchSessionSlot(conv, confirmConv.slotId, cfg[1]);
            });
            cf.appendChild(cb);
          });
          var cancelBtn = el("button", "up-cbtn", t("cancel"));
          cancelBtn.type = "button";
          cancelBtn.disabled = switchBusy;
          cancelBtn.addEventListener("click", function (ev) {
            if (ev && ev.stopPropagation) ev.stopPropagation();
            confirmConv = null;
            drawSessions();
            place();
          });
          cf.appendChild(cancelBtn);
          row.appendChild(cf);
        }
        body.appendChild(row);
      });
      box.appendChild(body);
    }
    function drawGroup1(now) {
      if (!popover) return;
      var g = popover.querySelector(".up-grp1");
      if (!g) return;
      g.textContent = "";
      rowCache = [];
      lastAppliedVisSig = null;
      var hd = el("div", "up-grp-h");
      hd.appendChild(el("span", "up-grp-n", "\u2460"));
      var titleWrap = el("span", "up-grp-t", t("group1Title"));
      titleWrap.appendChild(el("small", "", t("group1Hint")));
      hd.appendChild(titleWrap);
      g.appendChild(hd);
      var rows = el("div", "up-rows");
      var clRow = buildProviderRow(DEFS[0], now, true);
      if (clRow) {
        rows.appendChild(clRow);
        var b1 = buildRowBanner("claude");
        if (b1) rows.appendChild(b1);
      }
      var cxRow = buildProviderRow(DEFS[1], now, true);
      if (cxRow) {
        rows.appendChild(cxRow);
        var b2 = buildRowBanner("codex");
        if (b2) rows.appendChild(b2);
      }
      // Antigravity nested group (handles gemini + claude_gpt, or single bare agy entry, or harness_auth single_slot)
      var agyG = entryOf(DEFS[2]), agyC = entryOf(DEFS[3]);
      var agyBare = (data.entries || []).filter(function (e) { return e.harness === "agy" && !e.group; })[0] || null;
      var agyAuth = accountData && Array.isArray(accountData.harness_auth)
        ? accountData.harness_auth.filter(function (item) { return item && item.harness === "agy" && item.single_slot && (item.available || item.probed || item.auth_required); })[0] || null
        : null;
      if (agyG || agyC || agyBare || agyAuth) {
        var hg = el("div", "up-hg");
        hg.setAttribute("data-slot-harness", "agy");
        hg.setAttribute("data-readonly-slot", "1");
        var hh = el("div", "up-hg-h");
        var ab = el("span", "up-hb", "Ag"); ab.setAttribute("data-k", "agy");
        hh.appendChild(ab);
        hh.appendChild(el("span", "up-hr-name", "Antigravity"));
        var authReq = (agyAuth && agyAuth.auth_required) ||
          ((agyBare && agyBare.status === "sign-in required") && !agyG && !agyC) ||
          ((agyG && agyG.status === "sign-in required") && (!agyC || agyC.status === "sign-in required"));
        if (authReq) {
          hh.appendChild(buildAuthCta());
        } else {
          hh.appendChild(el("span", "up-pill ok sm dot", t("singleAccount")));
        }
        hg.appendChild(hh);
        var kids = el("div", "up-hg-kids");
        if (agyG) { var gr = buildProviderRow(DEFS[2], now, false); if (gr) kids.appendChild(gr); }
        if (agyC) { var cr = buildProviderRow(DEFS[3], now, false); if (cr) kids.appendChild(cr); }
        if (agyBare && !agyG && !agyC) {
          var bareRow = el("div", "up-hr");
          var bId = el("div", "up-hr-id"); bId.appendChild(el("span", "up-hr-name", localizedEntryName(agyBare)));
          bareRow.appendChild(bId);
          var bSt = el("div", "up-hr-status", statusText(agyBare.status));
          bareRow.appendChild(bSt);
          kids.appendChild(bareRow);
        }
        if (kids.children.length) hg.appendChild(kids);
        rows.appendChild(hg);
      }
      // Managed Pi (pi-rpc) compact switcher row without data-k so hover/click never
      // locks or dims Claude/Codex/Agy quota charts or rows.
      var piSlots = switcherOn ? slotsOf("pi-rpc") : [];
      if (switcherOn && piSlots.length > 0 && ((accountData && accountData.pi_runtime && accountData.pi_runtime.installed) || piSlots.length > 1)) {
        var piDef = defForHarness("pi-rpc", "");
        var piRow = el("div", "up-hr up-hr-noquota");
        piRow.setAttribute("data-slot-harness", "pi-rpc");
        var piId = el("div", "up-hr-id");
        if (piDef) piId.appendChild(hbadge(piDef, false));
        piId.appendChild(el("span", "up-hr-name", "Pi"));
        if (piSlots.length >= 2) {
          var piPills = buildSlotPills("pi-rpc");
          if (piPills) piId.appendChild(piPills);
        } else {
          var activePi = piSlots.filter(function (s) { return s.active; })[0] || piSlots[0];
          var piLbl = activePi.id === "default" ? t("defaultSlot") : (activePi.label || activePi.id);
          piId.appendChild(el("span", "up-pill sm", piLbl));
        }
        piRow.appendChild(piId);
        var piSt = el("div", "up-hr-status");
        piSt.appendChild(el("span", "", t("piNoQuotaNote")));
        if (piSlots.length < 2) {
          var piManage = el("a", "usage-manage-link", t("manageAccounts"));
          piManage.href = "/settings?section=account&provider=pi-rpc";
          if (openSettings) {
            piManage.addEventListener("click", function (ev) {
              if (ev && (ev.metaKey || ev.ctrlKey || ev.shiftKey || ev.altKey || ev.button)) return;
              if (ev && ev.preventDefault) ev.preventDefault();
              if (ev && ev.stopPropagation) ev.stopPropagation();
              openSettings("/settings?section=account&provider=pi-rpc", piManage);
            });
          }
          piSt.appendChild(piManage);
        }
        piRow.appendChild(piSt);
        rows.appendChild(piRow);
        var bPi = buildRowBanner("pi-rpc");
        if (bPi) rows.appendChild(bPi);
      }
      var sess = el("div", "up-sess");
      rows.appendChild(sess);
      g.appendChild(rows);
      drawSessions();
    }
    function drawGroup2() {
      if (!popover) return;
      var g = popover.querySelector(".up-grp2");
      if (!g) return;
      g.textContent = "";
      var hd = el("div", "up-grp-h");
      hd.appendChild(el("span", "up-grp-n", "\u2461"));
      hd.appendChild(el("span", "up-grp-t", t("group2Title")));
      g.appendChild(hd);
      var ph = el("div", "up-ph");
      ph.appendChild(el("b", "", t("group2Title")));
      ph.appendChild(el("span", "", t("group2Body")));
      ph.appendChild(el("span", "up-rsp"));
      ph.appendChild(el("span", "up-pill sm", t("placeholder")));
      g.appendChild(ph);
    }
    function cancelHoverRaf() {
      if (hoverRaf !== null && typeof window.cancelAnimationFrame === "function") {
        window.cancelAnimationFrame(hoverRaf);
      }
      hoverRaf = null;
      pendingHoverTarget = null;
    }
    function drawPopover() {
      if (!popover || !shown) return;
      cancelHoverRaf();
      lastHoverTarget = null;
      var now = Date.now();
      var scroll = popover.querySelector(".up-scroll");
      var emptyBox = popover.querySelector(".up-empty-state");
      drawSync(now);
      var hasEntries = (data.entries || []).length > 0;
      var hasSlots = !!(accountData && accountData.slots && Object.keys(accountData.slots).length > 0);
      var hasSingle = !!(accountData && Array.isArray(accountData.harness_auth) && accountData.harness_auth.some(function (x) { return x && x.single_slot && (x.available || x.probed || x.auth_required); }));
      if (!hasEntries && !hasSlots && !hasSingle) {
        chartCache = null;
        rowCache = null;
        lastAppliedVisSig = null;
        popover.setAttribute("data-cols", "0");
        if (scroll) scroll.hidden = true;
        if (emptyBox) {
          emptyBox.hidden = false;
          emptyBox.textContent = loadError ? t("unavailable") : t("notLoggedIn");
        }
        place();
        return;
      }
      popover.setAttribute("data-cols", String(Math.max(1, activeGroupDefs().length)));
      if (emptyBox) emptyBox.hidden = true;
      if (scroll) scroll.hidden = false;
      // Pill DOM nodes are rebuilt in drawGroup1, so mouseleave/blur on a replaced
      // pill will never fire and the previewed slot may have become active or gone.
      previewState = null;
      var deferCharts = hasResizeObserver && (popover.hidden || !(popover.clientWidth > 0));
      if (!deferCharts) drawCharts(now);
      drawGroup1(now);
      drawGroup2();
      applyVisualState();
      place();
      armReset();
    }
    function showTopBanner(text, isErr) {
      if (!popover) return;
      var b = popover.querySelector(".up-banner");
      if (!b) return;
      b.className = "up-banner" + (isErr ? " err" : "");
      b.textContent = text;
      b.hidden = false;
      place();
      if (bannerTimer !== null) window.clearTimeout(bannerTimer);
      bannerTimer = window.setTimeout(function () {
        bannerTimer = null;
        if (b) { b.hidden = true; place(); }
      }, 4500);
    }
    function setRowBanner(harness, text, tone) {
      banners[harness] = { text: text, tone: tone || "" };
      if (rowBannerTimers[harness]) window.clearTimeout(rowBannerTimers[harness]);
      rowBannerTimers[harness] = window.setTimeout(function () {
        delete banners[harness];
        delete rowBannerTimers[harness];
        if (shown) drawPopover();
      }, 7000);
    }
    function refresh(force) {
      if (pending) {
        if (force) forceQueued = true;
        return pending;
      }
      forceQueued = false;
      refreshing = true;
      if (shown) drawSync(Date.now());
      pending = window.fetch("/rail/usage?history=1" + (force ? "&refresh=1" : ""), {
        credentials: "same-origin", cache: "no-store",
        headers: force ? { "X-WS-Usage-Refresh": "1" } : {}
      }).then(function (r) {
        if (!r.ok) throw new Error("usage unavailable");
        return r.json();
      }).then(function (next) {
        loadError = false;
        data = next && Array.isArray(next.entries) ? next : { entries: [], warnPercent: 80, critPercent: 95 };
        refreshing = false;
        drawFooter();
        drawPopover();
        if (force) followUp();
      }).catch(function () {
        loadError = true;
        refreshing = false;
        data.entries.forEach(function (entry) { entry.status = "usage unavailable"; });
        drawPopover();
      }).then(function () {
        pending = null;
        if (forceQueued && !force) return refresh(true);
      });
      return pending;
    }
    function clearFollowUps() {
      followUps.forEach(function (id) { window.clearTimeout(id); });
      followUps = [];
    }
    function followUp() {
      clearFollowUps();
      if (!shown) return;
      followUps = [2000, 11000, 32000].map(function (ms) {
        return window.setTimeout(function () { if (shown && !doc.hidden) refresh(false); }, ms);
      });
    }
    function fetchAccountStatus() {
      if (!switcherOn) return Promise.resolve();
      if (accountPending) return accountPending;
      accountPending = window.fetch("/api/account/status", { credentials: "same-origin", cache: "no-store" }).then(function (r) {
        if (!r.ok) return null;
        return r.json();
      }).then(function (next) {
        accountData = next && typeof next === "object" && typeof next.loopback_peer === "boolean" ? next : null;
        drawPopover();
      }).catch(function () {
        accountData = null;
        drawPopover();
      }).then(function () {
        accountPending = null;
      });
      return accountPending;
    }
    function postAccountSwitch(path, body, onSuccess, onFail) {
      if (switchBusy) return;
      switchBusy = true;
      drawPopover();
      window.fetch("/api/account/" + path, {
        method: "POST", credentials: "same-origin", cache: "no-store",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body)
      }).then(function (r) {
        return r.json().catch(function () { return {}; }).then(function (res) {
          if (!r.ok) throw new Error(res.error || "switch_failed");
          return res;
        });
      }).then(function (res) {
        confirmConv = null;
        if (onSuccess) onSuccess(res);
        return Promise.all([fetchAccountStatus(), refresh(false)]);
      }).catch(function () {
        if (onFail) onFail();
        else showTopBanner(t("switchFailed"), true);
      }).then(function () {
        switchBusy = false;
        busyHarness = null;
        busySlotId = null;
        drawPopover();
      });
    }
    function switchHarnessSlot(harness, slotId) {
      busyHarness = harness;
      busySlotId = slotId;
      delete banners[harness];
      postAccountSwitch("slots/switch", { harness: harness, slot_id: slotId }, function (res) {
        var msg = res && res.queued_count > 0 ? t("switchedDefault", res.queued_count) : t("switchedDefaultOnly");
        setRowBanner(harness, msg, "");
        swapHarness = harness;
        if (swapTimer) window.clearTimeout(swapTimer);
        swapTimer = window.setTimeout(function () { swapHarness = null; swapTimer = null; }, 900);
      }, function () {
        setRowBanner(harness, t("switchFailed"), "warn");
      });
    }
    function switchSessionSlot(conv, slotId, modeVal) {
      postAccountSwitch("slots/switch", { conversation_id: conv.id, slot_id: slotId, mode: modeVal }, function () {
        showTopBanner(modeVal === "now" ? t("switchedNow") : t("switchedQueued"), false);
      }, function () {
        showTopBanner(t("switchFailed"), true);
      });
    }
    function setMode(nextMode) {
      if (nextMode !== "progress" && nextMode !== "timeline") return;
      mode = nextMode;
      if (!popover) return;
      queryAll(popover, "up-as-opt").forEach(function (btn) {
        var on = getAttr(btn, "data-v") === mode;
        toggleCls(btn, "is-on", on);
        btn.setAttribute("aria-selected", String(on));
      });
      drawCharts(Date.now());
      if (legendOpen) renderLegend();
      applyVisualState();
      place();
    }
    function closestEl(target, matcher) {
      var cur = target;
      while (cur && cur !== popover) {
        if (matcher(cur)) return cur;
        cur = cur.parentNode;
      }
      return null;
    }
    function hasAnyClass(node, list) {
      var cls = (node && node.className && typeof node.className === "string") ? node.className.split(/\s+/) :
        (node && typeof node.getAttribute === "function" ? (node.getAttribute("class") || "").split(/\s+/) : []);
      for (var i = 0; i < list.length; i++) {
        if (cls.indexOf(list[i]) >= 0) return true;
      }
      return false;
    }
    function makePopover(onPlace, onClose) {
      var rawPlace = onPlace || function () {};
      place = function () {
        rawPlace();
        if (popover) {
          lastPlacedW = popover.offsetWidth || 0;
          lastPlacedH = popover.offsetHeight || 0;
        }
      };
      var box = el("div", "rpop usage-pop");
      box.hidden = true;
      box.tabIndex = -1;
      box.setAttribute("role", "dialog");
      box.setAttribute("aria-label", t("heading"));

      // Header
      var hd = el("div", "up-hd");
      hd.appendChild(el("span", "up-title", t("heading")));
      var sync = el("span", "up-sync");
      sync.setAttribute("data-tone", "ok");
      sync.appendChild(el("span", "up-sdot"));
      sync.appendChild(el("span", "up-sync-t", t("syncOk")));
      hd.appendChild(sync);

      var seg = el("div", "up-axis-seg");
      seg.setAttribute("role", "tablist");
      [["progress", "modeProgress"], ["timeline", "modeTimeline"]].forEach(function (cfg) {
        var on = mode === cfg[0];
        var b = el("button", "up-as-opt" + (on ? " is-on" : ""), t(cfg[1]));
        b.type = "button";
        b.setAttribute("role", "tab");
        b.setAttribute("data-v", cfg[0]);
        b.setAttribute("aria-selected", String(on));
        b.addEventListener("click", function (ev) {
          if (ev && ev.stopPropagation) ev.stopPropagation();
          setMode(cfg[0]);
        });
        seg.appendChild(b);
      });
      hd.appendChild(seg);
      hd.appendChild(el("span", "up-rsp"));

      var refreshBtn = el("button", "up-ibtn up-refresh-btn");
      refreshBtn.type = "button";
      refreshBtn.setAttribute("aria-label", t("refresh"));
      refreshBtn.title = t("refresh");
      refreshBtn.appendChild(svgIcon(REFRESH_PATH, "up-ic"));
      refreshBtn.addEventListener("click", function (ev) {
        if (ev && ev.stopPropagation) ev.stopPropagation();
        refresh(true);
        if (switcherOn) fetchAccountStatus();
      });
      hd.appendChild(refreshBtn);

      var legendId = "usage-legend-" + (++chartSerial);
      var legendBtn = el("button", "up-ibtn up-legend-btn");
      legendBtn.type = "button";
      legendBtn.setAttribute("aria-label", t("legend"));
      legendBtn.setAttribute("aria-expanded", "false");
      legendBtn.setAttribute("aria-controls", legendId);
      legendBtn.title = t("legend");
      legendBtn.appendChild(svgIcon(HELP_PATH, "up-ic"));
      legendBtn.addEventListener("click", function (ev) {
        if (ev && ev.stopPropagation) ev.stopPropagation();
        legendOpen = !legendOpen;
        legendBtn.setAttribute("aria-expanded", String(legendOpen));
        legendBox.hidden = !legendOpen;
        if (legendOpen) renderLegend();
        place();
      });
      hd.appendChild(legendBtn);

      if (switcherOn) {
        var manage = el("a", "usage-manage-link", t("manageAccounts"));
        manage.href = "/settings?section=account";
        if (openSettings) {
          manage.addEventListener("click", function (e) {
            if (e && (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || e.button)) return;
            if (e && e.preventDefault) e.preventDefault();
            openSettings("/settings?section=account", manage);
          });
        }
        hd.appendChild(manage);
      }

      var close = el("button", "usage-close", "\u2715");
      close.type = "button";
      close.setAttribute("aria-label", t("close"));
      close.addEventListener("click", onClose);
      hd.appendChild(close);
      box.appendChild(hd);

      var topBanner = el("div", "up-banner");
      topBanner.setAttribute("role", "status");
      topBanner.hidden = true;
      box.appendChild(topBanner);

      var legendBox = el("div", "up-legend");
      legendBox.setAttribute("id", legendId);
      legendBox.hidden = true;
      box.appendChild(legendBox);

      var emptyBox = el("p", "usage-status up-empty-state");
      emptyBox.hidden = true;
      box.appendChild(emptyBox);

      var scroll = el("div", "up-scroll");
      scroll.appendChild(el("div", "up-charts"));
      scroll.appendChild(el("div", "up-grp1"));
      scroll.appendChild(el("div", "up-grp2"));
      box.appendChild(scroll);

      // Hover, focus, lock, and Esc delegation (zero SVG re-render, rAF-coalesced mouseover)
      function applyHoverTarget(targetEl) {
        if (focusLocked || previewState) return;
        var target = closestEl(targetEl, function (n) {
          return hasAnyClass(n, ["up-pin", "up-gl", "up-hr"]) && !!getAttr(n, "data-k") && getAttr(n, "tabindex") !== "-1";
        });
        var k = target ? getAttr(target, "data-k") : null;
        if (k !== focusKey) setFocus(k);
      }
      box.addEventListener("mouseover", function (e) {
        if (focusLocked || previewState) return;
        var tNode = e && e.target;
        if (tNode === lastHoverTarget) return;
        lastHoverTarget = tNode;
        if (typeof window.requestAnimationFrame !== "function") {
          applyHoverTarget(tNode);
          return;
        }
        pendingHoverTarget = tNode;
        if (hoverRaf !== null) return;
        hoverRaf = window.requestAnimationFrame(function () {
          hoverRaf = null;
          var tEl = pendingHoverTarget;
          pendingHoverTarget = null;
          applyHoverTarget(tEl);
        });
      });
      box.addEventListener("mouseleave", function () {
        cancelHoverRaf();
        lastHoverTarget = null;
        if (previewState) setPreview(null, null);
        if (!focusLocked && focusKey) setFocus(null);
      });
      box.addEventListener("focusin", function (e) {
        var pin = closestEl(e && e.target, function (n) { return hasAnyClass(n, ["up-pin"]) && !!getAttr(n, "data-k") && getAttr(n, "tabindex") !== "-1"; });
        if (pin && !focusLocked && !previewState) setFocus(getAttr(pin, "data-k"));
      });
      box.addEventListener("keydown", function (e) {
        if (!e) return;
        if (e.key === "Escape" && focusLocked) {
          if (e.preventDefault) e.preventDefault();
          if (e.stopPropagation) e.stopPropagation();
          unlockFocus();
          return;
        }
        var pin = closestEl(e.target, function (n) { return hasAnyClass(n, ["up-pin"]) && !!getAttr(n, "data-k"); });
        if (pin && (e.key === "Enter" || e.key === " ")) {
          if (e.preventDefault) e.preventDefault();
          toggleLock(getAttr(pin, "data-k"));
        }
      });
      box.addEventListener("click", function (e) {
        var interactive = closestEl(e && e.target, function (n) {
          var tag = n && n.tagName ? String(n.tagName).toLowerCase() : "";
          return tag === "button" || tag === "a";
        });
        if (interactive) return;
        var target = closestEl(e && e.target, function (n) {
          return hasAnyClass(n, ["up-pin", "up-gl", "up-hr"]) && !!getAttr(n, "data-k") && getAttr(n, "tabindex") !== "-1";
        });
        if (target) toggleLock(getAttr(target, "data-k"));
        else if (focusLocked) unlockFocus();
      });

      var rzTimer = null;
      if (hasResizeObserver) {
        new window.ResizeObserver(function () {
          var curW = box.clientWidth || 0;
          if (!shown || !curW || Math.abs(curW - lastChartW) < 4) return;
          if (scroll && scroll.hidden) return;
          var firstPaint = lastChartW === 0;
          lastChartW = curW;
          if (rzTimer !== null) { window.clearTimeout(rzTimer); rzTimer = null; }
          if (firstPaint) {
            drawCharts(Date.now()); applyVisualState(); place();
            return;
          }
          rzTimer = window.setTimeout(function () {
            rzTimer = null;
            if (shown) { drawCharts(Date.now()); applyVisualState(); place(); }
          }, 60);
        }).observe(box);
      }

      popover = box;
      return {
        el: box,
        open: function () {
          shown = true;
          cancelHoverRaf();
          lastHoverTarget = null;
          lastAppliedVisSig = null;
          lastPlacedW = 0;
          lastPlacedH = 0;
          confirmConv = null;
          focusKey = null;
          focusLocked = false;
          previewState = null;
          drawPopover();
          refresh(true);
          if (switcherOn) fetchAccountStatus();
        },
        close: function () {
          shown = false;
          cancelHoverRaf();
          lastHoverTarget = null;
          chartCache = null;
          rowCache = null;
          lastAppliedVisSig = null;
          lastPlacedW = 0;
          lastPlacedH = 0;
          lastChartW = 0;
          if (rzTimer !== null) { window.clearTimeout(rzTimer); rzTimer = null; }
          confirmConv = null;
          focusKey = null;
          focusLocked = false;
          previewState = null;
          clearFollowUps();
        },
        focus: function () { box.focus(); },
        back: function () {
          if (focusLocked) return unlockFocus();
          return false;
        }
      };
    }
    refresh(false);
    // Only connected, visible pages trigger periodic queries. The daemon
    // coalesces viewers and does no background polling with no page open.
    window.setInterval(function () { if (!doc.hidden) { drawFooter(); drawPopover(); refresh(false); } }, 300000);
    doc.addEventListener("visibilitychange", function () { if (!doc.hidden) refresh(false); });
    if (switcherOn) {
      doc.addEventListener("ws-account-changed", function () {
        if (shown) { fetchAccountStatus(); refresh(false); }
        else refresh(false);
      });
    }
    return { sync: drawFooter, makePopover: makePopover };
  } };
})(window);
