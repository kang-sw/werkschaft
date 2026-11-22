  // --- compact horizontal icon org-chart HUD -------------------------------
  var HUD_HARNESS_LABEL = { claude: "Cl", codex: "Co", agy: "Ag", pi: "Pi" };
  var HUD_HARNESS_NAME = { claude: "Claude", codex: "Codex", agy: "Antigravity", pi: "Pi" };
  var HUD_STATE_KEYS = {
    thinking: "js.hud.state.thinking",
    tool: "js.hud.state.tool",
    holding: "js.hud.state.holding",
    idle: "js.hud.state.idle",
    permission: "js.hud.state.permission",
    failed: "js.hud.state.failed"
  };
  var hudRegistry = {}, hudEdges = [], hudHoveredId = "", hudFoldedHovered = false, hudFoldedBtn = null, hudFoldedCount = 0, hudLead = "", hudBound = false, hudRO = null, hudRaf = 0;

  function hudCanControl() {
    return typeof root !== "undefined" && root.hasAttribute("data-can-control");
  }
  function hudClosed(replyId) {
    return typeof subagentClosing !== "undefined" && subagentClosing && subagentClosing[replyId] === "closed";
  }

  function hudStateText(st) {
    return wsT(HUD_STATE_KEYS[st] || "js.hud.state.idle");
  }
  function hudReadData(tree) {
    var el = tree.querySelector(".ag-source .ag-hud-data"), raw = {};
    if (el) { try { raw = JSON.parse(el.textContent || "{}") || {}; } catch (e) { raw = {}; } }
    return {
      lead: typeof raw.lead === "string" ? raw.lead : "",
      completed: Number(raw.completed) || 0,
      folded: Number(raw.folded) || 0,
      observationEnded: Number(raw.observationEnded) || 0,
      peers: Number(raw.peers) || 0,
      roots: Array.isArray(raw.roots) ? raw.roots : []
    };
  }
  function hudCollectOwnTasks(tree) {
    var tasks = {};
    q("details[data-key]", tree).forEach(function (d) {
      var k = d.getAttribute("data-key") || "";
      if (k.indexOf("agent-") !== 0) return;
      var id = k.slice(6), sib = d.nextElementSibling, liveOl = sib && sib.classList && sib.classList.contains("brows") && sib.classList.contains("live") ? sib : null, text = "";
      var live = liveOl && liveOl.querySelector ? (liveOl.querySelector(".brow.perm") || liveOl.querySelector(".brow.live")) : null;
      if (live) {
        var tn = live.querySelector(".tool"), args = live.querySelector(".args");
        text = [tn && tn.textContent.trim(), args && args.textContent.trim()].filter(Boolean).join(" ");
      }
      if (!text) {
        var cap = d.querySelector("summary.ahead .acap");
        if (cap) text = cap.textContent.trim();
      }
      if (text) tasks[id] = text;
    });
    return tasks;
  }
  function hudBuildNode() {
    var wrap = doc.createElement("div"); wrap.className = "tree-node";
    var slot = doc.createElement("div"); slot.className = "node-slot";
    var btn = doc.createElement("button"); btn.type = "button"; btn.className = "hud-node";
    var ring = doc.createElement("span"); ring.className = "hud-ring";
    var emb = doc.createElement("span"); emb.className = "hud-emblem";
    btn.appendChild(ring); btn.appendChild(emb);
    slot.appendChild(btn);
    wrap.appendChild(slot);
    return wrap;
  }
  function hudUpdateNode(wrap, node, ownTasks, moved) {
    var slot = wrap.firstElementChild, btn = slot.firstElementChild;
    var ring = btn.querySelector(".hud-ring"), emb = btn.querySelector(".hud-emblem");
    var state = node.state || "idle", harness = node.harness || "", label = node.label || node.id || "";
    var oldState = btn.getAttribute("data-state");
    btn.setAttribute("data-id", node.id || "");
    if (oldState !== state) btn.setAttribute("data-state", state);
    if (node.replyId) btn.setAttribute("data-reply-id", node.replyId); else btn.removeAttribute("data-reply-id");
    if (node.linked) btn.setAttribute("data-linked", "1"); else btn.removeAttribute("data-linked");
    if (node.native) btn.setAttribute("data-native", "1"); else btn.removeAttribute("data-native");
    btn.setAttribute("aria-label", label + " (" + (HUD_HARNESS_NAME[harness] || harness || "?") + ", " + hudStateText(state) + ")");
    if (harness) emb.setAttribute("data-h", harness); else emb.removeAttribute("data-h");
    emb.textContent = HUD_HARNESS_LABEL[harness] || "??";
    if (ANIMATION_PERIOD_MS[state]) animationPhaseDelay(ring, ANIMATION_PERIOD_MS[state], node.id || "", state, moved);
    else if (oldState !== state) { ring.style.animationDelay = ""; ring.wsAnimationStamp = ""; }
    var badge = btn.querySelector(".hud-badge"), badgeClass = state === "permission" ? "perm" : state === "failed" ? "fail" : "";
    if (badgeClass) {
      if (!badge) { badge = doc.createElement("span"); btn.appendChild(badge); }
      badge.className = "hud-badge " + badgeClass;
      badge.textContent = badgeClass === "perm" ? "!" : "\u00d7";
    } else if (badge) badge.remove();
    var overflow = slot.querySelector(".hud-overflow");
    if (node.overflow > 0) {
      if (!overflow) { overflow = doc.createElement("span"); overflow.className = "hud-overflow"; slot.appendChild(overflow); }
      overflow.textContent = "+" + node.overflow;
    } else if (overflow) overflow.remove();
    var close = slot.querySelector(".hud-close");
    if (node.replyId && hudCanControl() && !hudClosed(node.replyId)) {
      if (!close) {
        close = doc.createElement("button"); close.type = "button"; close.className = "hud-close";
        close.textContent = "\u00d7"; slot.appendChild(close);
      }
      var closeLabel = wsT(node.closeActive ? "js.subagent.close.active" : "js.subagent.close.ended", label);
      close.setAttribute("aria-label", closeLabel);
      close.setAttribute("title", closeLabel);
    } else if (close) close.remove();
    var entry = hudRegistry[node.id] || { data: {}, el: btn };
    Object.assign(entry.data, {
        id: node.id || "", replyId: node.replyId || "", linked: !!node.linked, label: label,
        harness: harness, state: state, model: node.model || "", effort: node.effort || "",
        closeActive: !!node.closeActive,
        elapsedMs: Number(node.elapsedMs) || 0, activeSince: Number(node.activeSince) || 0,
        chain: Array.isArray(node.chain) ? node.chain : [],
        task: node.task || ownTasks[node.id] || "", overflow: Number(node.overflow) || 0,
        native: !!node.native, startedAt: Number(node.startedAt) || 0, endedAt: Number(node.endedAt) || 0, endUnknown: !!node.endUnknown,
        contextTokens: Number(node.contextTokens) || 0, contextWindow: Number(node.contextWindow) || 0,
        contextSoft: Number(node.contextSoft) || 0, contextLevel: node.contextLevel || "", contextText: node.contextText || "",
        compacting: !!node.compacting, compactions: Number(node.compactions) || 0, compactOrigin: node.compactOrigin || ""
    });
    entry.el = btn;
    hudRegistry[node.id] = entry;
  }
  function hudReconcile(parent, nodes, existing, ownTasks, parentId, seen) {
    nodes.forEach(function (node, i) {
      var wrap = existing[node.id] || hudBuildNode();
      var moved = parent.children[i] !== wrap;
      if (moved) parent.insertBefore(wrap, parent.children[i] || null);
      hudUpdateNode(wrap, node, ownTasks, moved);
      seen[node.id] = true;
      if (parentId) hudEdges.push([parentId, node.id]);
      var children = Array.isArray(node.children) ? node.children : [];
      var kidsEl = wrap.querySelector(":scope > .tree-kids");
      if (children.length) {
        if (!kidsEl) { kidsEl = doc.createElement("div"); kidsEl.className = "tree-kids"; wrap.appendChild(kidsEl); }
        hudReconcile(kidsEl, children, existing, ownTasks, node.id, seen);
      } else if (kidsEl) kidsEl.remove();
    });
    for (var r = parent.children.length - 1; r >= nodes.length; r--) {
      var extra = parent.children[r];
      if (!extra.classList || !extra.classList.contains("hud-folded")) extra.remove();
    }
  }
  function hudFindFolded(rootsEl) {
    for (var i = 0; i < rootsEl.children.length; i++) {
      var child = rootsEl.children[i];
      if (child.classList && child.classList.contains("hud-folded")) return child;
    }
    return null;
  }
  function hudUpdateFolded(rootsEl, count) {
    var btn = hudFindFolded(rootsEl);
    if (count > 0) {
      if (!btn) {
        btn = doc.createElement("button");
        btn.type = "button";
        btn.className = "hud-folded";
        rootsEl.appendChild(btn);
      } else if (rootsEl.lastElementChild !== btn) {
        rootsEl.appendChild(btn);
      }
      var text = "+" + count;
      if (btn.textContent !== text) btn.textContent = text;
      var aria = wsT("js.hud.folded.aria", count);
      if (btn.getAttribute("aria-label") !== aria) btn.setAttribute("aria-label", aria);
      hudFoldedBtn = btn;
      hudFoldedCount = count;
    } else {
      if (btn) btn.remove();
      hudFoldedBtn = null;
      hudFoldedCount = 0;
    }
  }
  function hudDrawConnectors(agcompact) {
    var island = $("agisland");
    if (!agcompact || !island || island.hidden || island.getAttribute("data-mode") !== "compact") return;
    var canvas = agcompact.querySelector(".hud-canvas"), svg = agcompact.querySelector(".hud-lines");
    if (!canvas || !svg || !canvas.getBoundingClientRect) return;
    var cRect = canvas.getBoundingClientRect();
    if (!cRect.width || !cRect.height) return;
    svg.setAttribute("viewBox", "0 0 " + cRect.width + " " + cRect.height);
    svg.setAttribute("width", String(cRect.width));
    svg.setAttribute("height", String(cRect.height));
    var byParent = {}, order = [];
    hudEdges.forEach(function (edge) {
      if (!byParent[edge[0]]) { byParent[edge[0]] = []; order.push(edge[0]); }
      byParent[edge[0]].push(edge[1]);
    });
    var parts = [];
    order.forEach(function (pid) {
      var pEnt = hudRegistry[pid];
      if (!pEnt || !pEnt.el.getBoundingClientRect) return;
      var pr = pEnt.el.getBoundingClientRect();
      var px = Math.round(pr.left - cRect.left + pr.width / 2) + 0.5, py = Math.round(pr.bottom - cRect.top);
      var coords = [];
      byParent[pid].forEach(function (cid) {
        var cEnt = hudRegistry[cid];
        if (!cEnt || !cEnt.el.getBoundingClientRect) return;
        var cr = cEnt.el.getBoundingClientRect();
        coords.push({ cx: Math.round(cr.left - cRect.left + cr.width / 2) + 0.5, cy: Math.round(cr.top - cRect.top) });
      });
      if (!coords.length) return;
      var minCy = coords.reduce(function (m, c) { return Math.min(m, c.cy); }, coords[0].cy);
      var midY = Math.round((py + minCy) / 2) + 0.5;
      if (coords.length === 1) {
        var c0 = coords[0];
        parts.push(Math.abs(px - c0.cx) < 1 ? "M" + px + "," + py + "V" + c0.cy : "M" + px + "," + py + "V" + midY + "H" + c0.cx + "V" + c0.cy);
      } else {
        var minX = coords.reduce(function (m, c) { return Math.min(m, c.cx); }, coords[0].cx);
        var maxX = coords.reduce(function (m, c) { return Math.max(m, c.cx); }, coords[0].cx);
        parts.push("M" + px + "," + py + "V" + midY);
        parts.push("M" + minX + "," + midY + "H" + maxX);
        coords.forEach(function (c) { parts.push("M" + c.cx + "," + midY + "V" + c.cy); });
      }
    });
    svg.textContent = "";
    if (parts.length) {
      var p = doc.createElementNS(SVGNS, "path");
      p.setAttribute("d", parts.join(" "));
      svg.appendChild(p);
    }
  }
  function hudScheduleConnectors() {
    if (hudRaf || typeof requestAnimationFrame !== "function") return;
    hudRaf = requestAnimationFrame(function () {
      hudRaf = 0;
      hudDrawConnectors($("agcompact"));
      if (hudHoveredId) hudShowTooltip(hudHoveredId);
      else if (hudFoldedHovered) hudShowFoldedTooltip();
    });
  }
  function hudPositionTooltip(tt, btn) {
    if (!tt.getBoundingClientRect || !btn.getBoundingClientRect) return;
    var anchor = $("convo") || $("agisland");
    var vw = window.innerWidth || 800, vh = window.innerHeight || 600;
    var ar = anchor && anchor.getBoundingClientRect ? anchor.getBoundingClientRect() : null;
    var box = ar && ar.width > 0 && ar.height > 0 ? {
      left: Math.max(0, ar.left), top: Math.max(0, ar.top), right: Math.min(vw, ar.right), bottom: Math.min(vh, ar.bottom)
    } : { left: 0, top: 0, right: vw, bottom: vh };
    var br = btn.getBoundingClientRect(), tr = tt.getBoundingClientRect(), pad = 8;
    var left = br.left + br.width / 2 - tr.width / 2;
    left = Math.max(box.left + pad, Math.min(left, box.right - tr.width - pad));
    var top = br.bottom + 8;
    if (top + tr.height > box.bottom - pad) top = Math.max(box.top + pad, br.top - tr.height - 8);
    tt.style.left = Math.round(left) + "px";
    tt.style.top = Math.round(top) + "px";
  }
  function hudHideTooltip() {
    hudHoveredId = "";
    hudFoldedHovered = false;
    Object.keys(hudRegistry).forEach(function (k) {
      if (hudRegistry[k].el.classList) hudRegistry[k].el.classList.remove("is-hovered");
    });
    if (hudFoldedBtn && hudFoldedBtn.classList) hudFoldedBtn.classList.remove("is-hovered");
    var tt = $("hud-tooltip");
    if (tt) tt.hidden = true;
  }
  function hudShowFoldedTooltip() {
    var tt = $("hud-tooltip");
    if (!tt || !hudFoldedBtn || hudFoldedCount <= 0) { hudHideTooltip(); return; }
    hudHoveredId = "";
    hudFoldedHovered = true;
    Object.keys(hudRegistry).forEach(function (k) {
      if (hudRegistry[k].el.classList) hudRegistry[k].el.classList.remove("is-hovered");
    });
    if (hudFoldedBtn.classList) hudFoldedBtn.classList.add("is-hovered");
    tt.textContent = "";
    var head = doc.createElement("div"); head.className = "tt-head";
    var title = doc.createElement("span"); title.className = "tt-title"; title.textContent = wsT("js.hud.folded.title", hudFoldedCount);
    var pill = doc.createElement("span"); pill.className = "tt-pill"; pill.setAttribute("data-state", "idle"); pill.textContent = "+" + hudFoldedCount;
    head.appendChild(title); head.appendChild(pill);
    tt.appendChild(head);
    var hint = doc.createElement("div"); hint.className = "tt-hint";
    hint.textContent = wsT("js.hud.hint.details");
    tt.appendChild(hint);
    tt.hidden = false;
    hudPositionTooltip(tt, hudFoldedBtn);
  }
  function hudShowTooltip(id) {
    var tt = $("hud-tooltip"), entry = hudRegistry[id];
    if (!tt || !entry) { hudHideTooltip(); return; }
    hudHoveredId = id;
    hudFoldedHovered = false;
    Object.keys(hudRegistry).forEach(function (k) {
      if (hudRegistry[k].el.classList) hudRegistry[k].el.classList.toggle("is-hovered", hudRegistry[k].el === entry.el);
    });
    if (hudFoldedBtn && hudFoldedBtn.classList) hudFoldedBtn.classList.remove("is-hovered");
    var d = entry.data;
    tt.textContent = "";
    var head = doc.createElement("div"); head.className = "tt-head";
    var hb = doc.createElement("span"); hb.className = "hb sm";
    if (d.harness) hb.setAttribute("data-h", d.harness);
    var title = doc.createElement("span"); title.className = "tt-title"; title.textContent = d.label;
    var pill = doc.createElement("span"); pill.className = "tt-pill"; pill.setAttribute("data-state", d.state); pill.textContent = hudStateText(d.state);
    head.appendChild(hb); head.appendChild(title);
    if (d.native) { var nb = doc.createElement("span"); nb.className = "tt-native"; nb.textContent = wsT("js.hud.native"); head.appendChild(nb); }
    head.appendChild(pill);
    tt.appendChild(head);
    if (d.native) tt.appendChild(hudLifecycle(d));
    var meta = doc.createElement("div"); meta.className = "tt-meta";
    if (d.model) { var mc = doc.createElement("code"); mc.textContent = d.model; meta.appendChild(mc); }
    if (d.effort) { var ef = doc.createElement("span"); ef.textContent = d.effort; meta.appendChild(ef); }
    var elSpan = doc.createElement("span");
    if (meta.childNodes.length) elSpan.appendChild(doc.createTextNode("\u00b7 "));
    if (d.activeSince > 0) {
      var tm = doc.createElement("time"); tm.setAttribute("data-ms", String(d.activeSince)); tm.setAttribute("data-f", "since");
      if (typeof fmtTime === "function") fmtTime(tm, Date.now());
      elSpan.appendChild(tm);
    } else {
      elSpan.appendChild(doc.createTextNode(typeof dur === "function" ? dur(d.elapsedMs) : String(d.elapsedMs) + "ms"));
    }
    if (d.overflow > 0) elSpan.appendChild(doc.createTextNode(" \u00b7 " + wsT("js.hud.deeper", d.overflow)));
    meta.appendChild(elSpan); tt.appendChild(meta);
    var ctx = hudContextRow(d); if (ctx) tt.appendChild(ctx);
    var cmp = hudCompactionRow(d); if (cmp) tt.appendChild(cmp);
    var chain = doc.createElement("div"); chain.className = "tt-chain";
    chain.textContent = [hudLead].concat(d.chain, [d.label]).filter(Boolean).join(" \u203a ");
    tt.appendChild(chain);
    if (d.task) { var task = doc.createElement("div"); task.className = "tt-task"; task.textContent = d.task; tt.appendChild(task); }
    var hint = doc.createElement("div"); hint.className = "tt-hint";
    hint.textContent = d.linked && d.replyId ? wsT("js.hud.hint.link", d.replyId) : wsT("js.hud.hint.details");
    tt.appendChild(hint);
    tt.hidden = false;
    hudPositionTooltip(tt, entry.el);
  }
  // hudContextRow is the card's context occupancy row: percent, bar, ws soft
  // tick and "used / window" text when the window is known; the bare token
  // figure when it is not; nothing when the tokens are not known either. The
  // colour class (contextLevel) is the status band's, computed server-side.
  function hudContextRow(d) {
    if (!(d.contextTokens > 0)) return null;
    var row = doc.createElement("div"); row.className = "tt-ctx";
    if (!(d.contextWindow > 0)) {
      row.classList.add("d-unknown");
      var only = doc.createElement("span"); only.className = "tok"; only.textContent = wsT("js.hud.context", d.contextText);
      row.appendChild(only);
      return row;
    }
    if (d.contextLevel) row.classList.add(d.contextLevel);
    var raw = d.contextTokens / d.contextWindow * 100;
    var pct = doc.createElement("span"); pct.className = "pct"; pct.textContent = Math.round(raw) + "%";
    var bar = doc.createElement("span"); bar.className = "bar";
    bar.setAttribute("role", "progressbar"); bar.setAttribute("aria-valuemin", "0"); bar.setAttribute("aria-valuemax", "100");
    bar.setAttribute("aria-valuenow", String(Math.min(100, Math.max(0, Math.round(raw)))));
    var fill = doc.createElement("span"); fill.className = "fill"; fill.style.width = Math.min(100, Math.max(0, raw)).toFixed(1) + "%";
    bar.appendChild(fill);
    if (d.contextSoft > 0 && d.contextSoft < d.contextWindow) {
      var soft = doc.createElement("span"); soft.className = "m-soft"; soft.style.left = (d.contextSoft / d.contextWindow * 100).toFixed(1) + "%";
      bar.appendChild(soft);
    }
    var tok = doc.createElement("span"); tok.className = "tok"; tok.textContent = d.contextText;
    row.appendChild(pct); row.appendChild(bar); row.appendChild(tok);
    return row;
  }
  // hudCompactionRow is the card's compaction line under the context row: an
  // accent chip while a compaction is queued or running, then the count and the
  // latest compaction's origin when known. No line without either.
  function hudCompactionRow(d) {
    if (!d.compacting && !(d.compactions > 0)) return null;
    var row = doc.createElement("div"); row.className = "tt-cmp";
    if (d.compacting) {
      var now = doc.createElement("span"); now.className = "d-now"; now.textContent = wsT("js.hud.compacting");
      row.appendChild(now);
    }
    if (d.compactions > 0) {
      var cnt = doc.createElement("span"); cnt.className = "cnt";
      var text = wsT("js.hud.compactions", d.compactions);
      var origin = d.compactOrigin === "auto" ? wsT("js.hud.compactorigin.auto") : d.compactOrigin === "manual" ? wsT("js.hud.compactorigin.manual") : d.compactOrigin === "native" ? wsT("js.hud.compactorigin.native") : "";
      if (origin) text += " \u00b7 " + origin;
      cnt.textContent = text;
      row.appendChild(cnt);
    }
    return row;
  }
  // hudLifecycle is a native row's start and end; a row whose observation
  // ended with the outcome unknown (endUnknown) reads as such, never as a
  // completion.
  function hudLifecycle(d) {
    var life = doc.createElement("div"); life.className = "tt-life";
    function stamp(key, ms) {
      var span = doc.createElement("span");
      span.appendChild(doc.createTextNode(wsT(key) + " "));
      var tm = doc.createElement("time"); tm.setAttribute("data-ms", String(ms)); tm.setAttribute("data-f", "clock");
      if (typeof fmtTime === "function") fmtTime(tm, Date.now()); else tm.textContent = new Date(ms).toLocaleTimeString();
      span.appendChild(tm);
      return span;
    }
    if (d.startedAt > 0) life.appendChild(stamp("js.hud.started", d.startedAt));
    var terminal = d.state === "idle" || d.state === "failed";
    if (terminal && !d.endUnknown && d.endedAt > 0) {
      if (life.childNodes.length) life.appendChild(doc.createTextNode(" \u00b7 "));
      life.appendChild(stamp("js.hud.ended", d.endedAt));
    } else if (terminal && d.endUnknown) {
      var unk = doc.createElement("span"); unk.className = "tt-unknown"; unk.textContent = wsT("js.hud.unknownend");
      if (life.childNodes.length) life.appendChild(doc.createTextNode(" \u00b7 "));
      life.appendChild(unk);
    }
    return life;
  }
  // hudObservationEnded shows, in a view (the compact HUD or the idle
  // island), how many native rows ended with their outcome unknown; they are
  // not in the completed count.
  function hudObservationEnded(view, holder, n) {
    if (!view) return;
    var host = holder || view, chip = null;
    for (var i = 0; i < host.children.length; i++) {
      if (host.children[i].classList && host.children[i].classList.contains("hud-obs")) chip = host.children[i];
    }
    if (n > 0) {
      if (!chip) { chip = doc.createElement("span"); chip.className = "hud-obs"; host.appendChild(chip); }
      chip.textContent = wsT("js.hud.obsended", n);
      chip.setAttribute("title", wsT("js.hud.obsended.title"));
    } else if (chip) chip.remove();
  }
  function hudFocusNode(id, agpop, agOpenFn) {
    var target = null;
    q("details[data-key]", agpop).forEach(function (d) {
      if (!target && d.getAttribute("data-key") === "agent-" + id) target = d;
    });
    if (target) {
      var cur = target;
      while (cur && cur !== agpop) {
        if (cur.tagName === "DETAILS") cur.open = true;
        cur = cur.parentNode;
      }
    }
    agOpenFn(true);
    if (target) {
      var sum = target.querySelector("summary");
      if (sum && sum.scrollIntoView) sum.scrollIntoView({ block: "nearest" });
      if (sum && sum.focus) sum.focus();
    }
  }
  function hudBindEvents(agcompact, agpop, agOpenFn) {
    if (hudBound || !agcompact) return;
    hudBound = true;
    var rootsEl = agcompact.querySelector(".hud-roots");
    var canvas = agcompact.querySelector(".hud-canvas"), island = $("agisland");
    agcompact.addEventListener("click", function (e) {
      if (e.target && e.target.closest && (e.target.closest(".hud-node") || e.target.closest(".hud-close") || e.target.closest(".hud-folded"))) return;
      agOpenFn(true);
    });
    agcompact.addEventListener("keydown", function (e) {
      if (e.target === agcompact && (e.key === "Enter" || e.key === " ")) { e.preventDefault(); agOpenFn(true); }
    });
    if (rootsEl) {
      rootsEl.addEventListener("mouseover", function (e) {
        var folded = e.target && e.target.closest && e.target.closest(".hud-folded");
        if (folded) { hudShowFoldedTooltip(); return; }
        var btn = e.target && e.target.closest && e.target.closest(".hud-node");
        if (btn) hudShowTooltip(btn.getAttribute("data-id"));
      });
      rootsEl.addEventListener("mouseout", function (e) {
        var folded = e.target && e.target.closest && e.target.closest(".hud-folded");
        if (folded && (!e.relatedTarget || !folded.contains(e.relatedTarget))) { hudHideTooltip(); return; }
        var btn = e.target && e.target.closest && e.target.closest(".hud-node");
        if (btn && (!e.relatedTarget || !btn.contains(e.relatedTarget))) hudHideTooltip();
      });
      rootsEl.addEventListener("focusin", function (e) {
        var folded = e.target && e.target.closest && e.target.closest(".hud-folded");
        if (folded) { hudShowFoldedTooltip(); return; }
        var btn = e.target && e.target.closest && e.target.closest(".hud-node");
        if (btn) hudShowTooltip(btn.getAttribute("data-id"));
      });
      rootsEl.addEventListener("focusout", function (e) {
        if (!e.relatedTarget || !rootsEl.contains(e.relatedTarget)) hudHideTooltip();
      });
      rootsEl.addEventListener("click", function (e) {
        var folded = e.target && e.target.closest && e.target.closest(".hud-folded");
        if (folded) {
          if (e.preventDefault) e.preventDefault();
          if (e.stopPropagation) e.stopPropagation();
          hudHideTooltip();
          agOpenFn(true);
          return;
        }
        var close = e.target && e.target.closest && e.target.closest(".hud-close");
        if (close) {
          e.preventDefault(); e.stopPropagation();
          var owner = close.parentNode && close.parentNode.querySelector(".hud-node");
          var target = owner && hudRegistry[owner.getAttribute("data-id")];
          if (hudCanControl() && target && target.data.replyId && typeof subagentClose === "function") {
            hudHideTooltip();
            var state = target.data.state;
            if (target.data.closeActive && (state === "failed" || state === "idle")) state = "holding";
            subagentClose(target.data.replyId, target.data.label, state, close);
          }
          return;
        }
        var btn = e.target && e.target.closest && e.target.closest(".hud-node");
        if (!btn) return;
        var id = btn.getAttribute("data-id"), entry = hudRegistry[id];
        if (entry && entry.data.linked && entry.data.replyId) {
          hudHideTooltip();
          var href = "/s/" + encodeURIComponent(entry.data.replyId);
          if (typeof go === "function") go(href);
          else if (window.location) window.location.href = href;
          return;
        }
        hudFocusNode(id, agpop, agOpenFn);
      });
    }
    if (agpop) agpop.addEventListener("click", function (e) {
      var close = e.target && e.target.closest && e.target.closest(".aclose");
      if (!close) return;
      e.preventDefault(); e.stopPropagation();
      if (hudCanControl() && typeof subagentClose === "function") {
        subagentClose(close.getAttribute("data-reply-id"), close.getAttribute("data-label"), close.getAttribute("data-state"), close);
      }
    });
    doc.addEventListener("ws:subagent-closed", function (e) {
      var rid = e.detail && e.detail.replyId;
      q(".hud-close", rootsEl).forEach(function (el) {
        var owner = el.parentNode && el.parentNode.querySelector(".hud-node");
        if (owner && owner.getAttribute("data-reply-id") === rid) el.remove();
      });
      q(".aclose", agpop).forEach(function (el) { if (el.getAttribute("data-reply-id") === rid) el.remove(); });
    });
    if (window.ResizeObserver && canvas) { hudRO = new ResizeObserver(hudScheduleConnectors); hudRO.observe(canvas); }
    if (island) island.addEventListener("transitionend", function (e) {
      if (e.target === island && (e.propertyName === "width" || e.propertyName === "height")) hudScheduleConnectors();
    });
  }
  function hudProject(tree, agcompact, agidle, agpop, agOpenFn) {
    var hud = hudReadData(tree), ownTasks = hudCollectOwnTasks(tree);
    q(".aclose", agpop).forEach(function (el) { el.hidden = !hudCanControl() || hudClosed(el.getAttribute("data-reply-id")); });
    hudLead = hud.lead || wsT("js.hud.lead");
    hudEdges = [];
    var rootsEl = agcompact.querySelector(".hud-roots");
    if (rootsEl) {
      var existing = Object.create(null);
      q(".tree-node > .node-slot > .hud-node", rootsEl).forEach(function (btn) {
        existing[btn.getAttribute("data-id")] = btn.parentElement.parentElement;
      });
      var seen = Object.create(null);
      hudReconcile(rootsEl, hud.roots, existing, ownTasks, "", seen);
      Object.keys(hudRegistry).forEach(function (id) { if (!seen[id]) delete hudRegistry[id]; });
      hudUpdateFolded(rootsEl, hud.roots.length > 0 ? hud.folded : 0);
    }
    hudObservationEnded(agcompact, agcompact.querySelector(".hud-canvas"), hud.observationEnded);
    hudObservationEnded(agidle, null, hud.observationEnded);
    hudBindEvents(agcompact, agpop, agOpenFn);
    if (hudHoveredId) {
      if (hudRegistry[hudHoveredId] && agpop && agpop.hidden) hudShowTooltip(hudHoveredId);
      else hudHideTooltip();
    } else if (hudFoldedHovered) {
      if (hudFoldedBtn && hudFoldedCount > 0 && agpop && agpop.hidden) hudShowFoldedTooltip();
      else hudHideTooltip();
    }
    hudScheduleConnectors();
    return { visibleCount: hud.roots.length, folded: hud.folded, completed: hud.completed, observationEnded: hud.observationEnded, peers: hud.peers };
  }
  window.wsHud = { project: hudProject, drawConnectors: hudScheduleConnectors, hideTooltip: hudHideTooltip };
