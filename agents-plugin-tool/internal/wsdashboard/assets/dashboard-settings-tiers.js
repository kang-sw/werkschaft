// Tier routing editor (agent.tier_routing) for the settings page.
//
// The pointer DAG is drawn procedurally from the daemon's description of
// the graph (GET /api/config/tier-routing): every node's edges, its depth
// (hops to a terminal), inbound references, and per-size traces come from
// the daemon's own graph and resolver, so this page never re-implements
// resolution. A node with no "*" pointer has an implicit "caller" edge: its
// requested size, as the bare size, on the caller's harness. That edge ends
// at its own terminal, so the fallthrough is visible apart from an explicit
// pointer to the same harness. A harness agent.spawn cannot launch now (the
// daemon's availability check) is marked on its terminal, on the nodes whose
// route ends there, and in their traces, which then carry no binding. An
// edit becomes a draft the daemon previews
// (POST .../preview) before it can be saved through /api/config/set, which
// validates again; cycles and the hop limit show inline and leave the
// stored graph in force. A save carries the revision of the graph it was
// built from, and the daemon refuses it once that graph has changed (another
// tab saved meanwhile), so a stale copy never replaces a newer graph. The graph is global-only (one graph for every
// worktree). A node or bundle that pointers still reference cannot be
// deleted; the editor lists those references instead.
(function () {
  "use strict";

  const tr = (...args) => window.wsT(...args);
  const SIZE_SHORT = { small: "S", medium: "M", large: "L", xlarge: "XL" };
  const KIND_ORDER = { group: 0, role: 1, user: 2 };
  const CALLER_KEY = "ws.settings.tierCaller";
  const KIND_KEY = { group: "js.settings.tier_routing.kind.group", role: "js.settings.tier_routing.kind.role", user: "js.settings.tier_routing.kind.user", bundle: "js.settings.tier_routing.kind.bundle" };
  const GROUP_KEY = { nodes: "js.settings.tier_routing.group.nodes", harnesses: "js.settings.tier_routing.group.harnesses", bundles: "js.settings.tier_routing.group.bundles" };

  function el(tag, cls, text) {
    const node = document.createElement(tag);
    if (cls) node.className = cls;
    if (text !== undefined) node.textContent = text;
    return node;
  }

  function btn(cls, text, handler) {
    const node = el("button", cls, text);
    node.type = "button";
    if (handler) node.addEventListener("click", handler);
    return node;
  }

  function option(value, text) {
    const node = el("option", "", text);
    node.value = value;
    return node;
  }

  async function call(path, body) {
    const opts = { credentials: "same-origin", cache: "no-store" };
    if (body !== undefined) {
      Object.assign(opts, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
    }
    let response, json = null;
    try {
      response = await fetch(path, opts);
    } catch (error) {
      return { ok: false, code: "config_error", error: "" };
    }
    try { json = await response.json(); } catch (error) { /* an older daemon answers plain text */ }
    if (response.ok && json) return { ok: true, json };
    const failure = json && json.error;
    return {
      ok: false,
      code: failure && failure.code || (response.status === 404 ? "upgrade_required" : "config_error"),
      error: failure && failure.message || ""
    };
  }

  function clone(value) { return JSON.parse(JSON.stringify(value || {})); }

  // routingOf is an editable copy of the view's stored overrides.
  function routingOf(view) {
    const routing = clone(view.routing);
    routing.nodes = routing.nodes || {};
    routing.bundles = routing.bundles || {};
    return routing;
  }

  // compact drops empty members so a draft compares and stores canonically.
  function compact(routing) {
    const out = {};
    if (routing.nodes && Object.keys(routing.nodes).length) out.nodes = routing.nodes;
    if (routing.bundles && Object.keys(routing.bundles).length) out.bundles = routing.bundles;
    return out;
  }

  function canonical(value) {
    if (Array.isArray(value)) return value.map(canonical);
    if (value && typeof value === "object") {
      const out = {};
      Object.keys(value).sort().forEach(function (key) { out[key] = canonical(value[key]); });
      return out;
    }
    return value;
  }

  function same(a, b) { return JSON.stringify(canonical(a)) === JSON.stringify(canonical(b)); }

  function decode(pointer) {
    if (!pointer) return { kind: "none", name: "", size: "*" };
    let i = pointer.indexOf(":");
    if (i >= 0) return { kind: "node", name: pointer.slice(0, i), size: pointer.slice(i + 1) };
    i = pointer.indexOf(".");
    if (i >= 0) return { kind: "harness", name: pointer.slice(0, i), size: pointer.slice(i + 1) };
    return { kind: "bundle", name: pointer, size: "" };
  }

  function encode(target) {
    if (target.kind === "node") return target.name + ":" + (target.size || "*");
    if (target.kind === "harness") return target.name + "." + (target.size || "*");
    if (target.kind === "bundle") return target.name;
    return "";
  }

  function edgeTargetId(edge) {
    if (edge.kind === "node") return "n:" + edge.to;
    if (edge.kind === "harness") return "h:" + edge.to;
    if (edge.kind === "bundle") return "b:" + edge.to;
    return "caller";
  }

  // edgeClass names how an edge is drawn: the caller fallthrough, a size
  // exception, a pointer to a bundle, the user's own pointer, or a packaged
  // default.
  function edgeClass(node, edge) {
    if (edge.kind === "caller") return "fall";
    if (edge.selector !== "*") return "exc";
    if (edge.kind === "bundle") return "bundle";
    if (node.overrides && Object.prototype.hasOwnProperty.call(node.overrides, edge.selector)) return "user";
    return "default";
  }

  function refsText(refs) {
    return refs.map(function (ref) { return ref.replace(" ", " · "); }).join(", ");
  }

  // layout places nodes in columns by depth (deepest left, terminals right)
  // and orders each column by the mean position of its targets, so the
  // daemon's edges cross as little as a simple pass allows.
  function layout(view) {
    const terminals = [], seen = {};
    function addTerminal(id, info) {
      if (seen[id]) return;
      seen[id] = true;
      terminals.push(Object.assign({ id: id }, info));
    }
    view.harnesses.forEach(function (harness) {
      if (view.nodes.some(function (n) { return n.edges.some(function (e) { return e.kind === "harness" && e.to === harness; }); })) {
        addTerminal("h:" + harness, { kind: "harness", name: harness });
      }
    });
    view.bundles.forEach(function (bundle) { addTerminal("b:" + bundle.name, { kind: "bundle", name: bundle.name, bundle: bundle }); });
    if (view.nodes.some(function (n) { return n.edges.some(function (e) { return e.kind === "caller"; }); })) {
      addTerminal("caller", { kind: "caller", name: view.harness });
    }
    const max = view.nodes.reduce(function (m, n) { return Math.max(m, n.depth); }, 1);
    const columns = [];
    for (let d = 0; d <= max; d++) columns.push([]);
    const pos = {};
    terminals.forEach(function (term, i) { pos[term.id] = (i + 0.5) / Math.max(terminals.length, 1); });
    for (let d = 1; d <= max; d++) {
      const list = view.nodes.filter(function (n) { return n.depth === d; });
      const score = {};
      list.forEach(function (n) {
        const ys = n.edges.map(function (e) { return pos[edgeTargetId(e)]; }).filter(function (y) { return y !== undefined; });
        score[n.name] = ys.length ? ys.reduce(function (a, b) { return a + b; }, 0) / ys.length : 0.5;
      });
      list.sort(function (a, b) {
        return score[a.name] - score[b.name] || KIND_ORDER[a.kind] - KIND_ORDER[b.kind] || (a.name < b.name ? -1 : 1);
      });
      list.forEach(function (n, i) { pos["n:" + n.name] = (i + 0.5) / list.length; });
      columns[d] = list;
    }
    return { terminals: terminals, columns: columns, max: max };
  }

  function storedCaller() {
    try { return localStorage.getItem(CALLER_KEY) || ""; } catch (error) { return ""; }
  }

  function render(ctx) {
    const state = { view: null, harness: storedCaller(), focus: "", failure: "", notice: "", resetArmed: false };
    const card = el("section", "sgroup tr-card");
    card.dataset.configKey = ctx.entry.key;
    card.dataset.tierRouting = "1";
    const head = el("div", "tr-head");
    const labelLine = el("div", "klabel-line");
    labelLine.append(el("b", "klabel", ctx.label), el("code", "skey", ctx.entry.key));
    const scopePill = el("span", "tag tr-scope", tr("js.settings.tier_routing.scope"));
    scopePill.dataset.tierScope = "global";
    labelLine.append(scopePill);
    if (!ctx.editable) labelLine.append(el("span", "tag lock", ctx.lockText));
    const status = el("span", "tr-status");
    status.setAttribute("role", "status");
    labelLine.append(status);
    head.append(labelLine, el("p", "kdesc", ctx.description));
    const body = el("div", "tr-body");
    card.append(head, body);
    let observer = null;
    // loads counts graph reads: only the latest one may replace the view, so
    // a slow answer for an earlier caller harness cannot undo a later switch.
    let loads = 0;
    load();
    return card;

    async function load() {
      const mine = ++loads;
      const result = await call("/api/config/tier-routing?harness=" + encodeURIComponent(state.harness || ""));
      if (mine !== loads) return;
      if (!result.ok) {
        state.failure = result.code;
        draw();
        return;
      }
      state.failure = "";
      state.view = result.json;
      state.harness = result.json.harness;
      if (!state.focus || !findNode(state.focus)) {
        const first = state.view.nodes.find(function (n) { return n.kind === "role"; }) || state.view.nodes[0];
        state.focus = first ? first.name : "";
      }
      draw();
    }

    function findNode(name) { return state.view && state.view.nodes.find(function (n) { return n.name === name; }); }
    function findBundle(name) { return state.view && state.view.bundles.find(function (b) { return b.name === name; }); }

    function draw() {
      body.replaceChildren();
      if (observer) { observer.disconnect(); observer = null; }
      if (!state.view) {
        status.textContent = "";
        const note = el("p", "info-note", state.failure ? tr("js.settings.error." + state.failure) : tr("js.settings.tier_routing.loading"));
        note.setAttribute("role", state.failure ? "alert" : "status");
        body.append(note);
        return;
      }
      const view = state.view, plan = layout(view);
      status.className = "tr-status " + (view.error ? "bad" : "ok");
      status.textContent = view.error ? tr("js.settings.tier_routing.invalid", view.error) : tr("js.settings.tier_routing.valid", plan.max, view.max_hops);
      body.append(toolbar(view));
      if (state.notice) {
        const notice = el("p", "inline-warn tr-notice", state.notice);
        notice.setAttribute("role", "alert");
        body.append(notice);
      }
      if (view.collisions.length) {
        const warn = el("div", "inline-warn tr-collisions");
        warn.append(el("b", "", tr("js.settings.tier_routing.collisions")), el("span", "", " " + view.collisions.join("; ")));
        body.append(warn);
      }
      const frame = el("div", "tr-frame");
      const grid = el("div", "tr-grid");
      grid.style.gridTemplateColumns = "repeat(" + plan.max + ", 244px) 268px";
      const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
      svg.setAttribute("class", "tr-edges");
      svg.setAttribute("aria-hidden", "true");
      for (let d = plan.max; d >= 0; d--) {
        const col = el("div", "tr-col");
        col.dataset.depth = String(d);
        const count = d === 0 ? plan.terminals.length : plan.columns[d].length;
        const hd = el("div", "tr-colhd");
        hd.append(el("b", "", d === 0 ? tr("js.settings.tier_routing.depth.terminal") : tr("js.settings.tier_routing.depth", d)), el("span", "", d === 0 ? String(count) : tr("js.settings.tier_routing.hops", d) + " (" + count + ")"));
        col.append(hd);
        if (d === 0) plan.terminals.forEach(function (term) { col.append(terminalCard(view, term)); });
        else plan.columns[d].forEach(function (node) { col.append(nodeCard(view, node)); });
        grid.append(col);
      }
      frame.append(svg, grid);
      body.append(frame, traceBar(view));
      const paint = function () { drawEdges(view, frame, svg); };
      requestAnimationFrame(paint);
      if (typeof ResizeObserver === "function") {
        observer = new ResizeObserver(function () { requestAnimationFrame(paint); });
        observer.observe(grid);
      }
    }

    function toolbar(view) {
      const bar = el("div", "tr-toolbar");
      if (ctx.editable) {
        bar.append(btn("btn pri tr-add-node", tr("js.settings.tier_routing.add_node"), function () { openNode(null); }));
        bar.append(btn("btn tr-add-bundle", tr("js.settings.tier_routing.add_bundle"), function () { openBundle(null); }));
        if (Object.keys(compact(routingOf(view))).length) {
          const reset = btn("btn tr-reset-all", state.resetArmed ? tr("js.settings.tier_routing.reset_confirm") : tr("js.settings.tier_routing.reset_all"), async function () {
            if (!state.resetArmed) { state.resetArmed = true; reset.textContent = tr("js.settings.tier_routing.reset_confirm"); return; }
            state.resetArmed = false;
            reset.disabled = true;
            const saved = await save({}, view.revision, null);
            if (!saved) reset.disabled = false;
          });
          bar.append(reset);
        }
      } else {
        bar.append(el("span", "hint", tr("js.settings.tier_routing.read_only")));
      }
      const callerBox = el("div", "tr-caller");
      callerBox.append(el("span", "blbl", tr("js.settings.tier_routing.caller")));
      const seg = el("div", "seg bucket-seg");
      seg.setAttribute("role", "group");
      seg.setAttribute("aria-label", tr("js.settings.tier_routing.caller"));
      view.harnesses.forEach(function (harness) {
        const b = btn("", harness, function () {
          state.harness = harness;
          try { localStorage.setItem(CALLER_KEY, harness); } catch (error) { /* storage may be unavailable */ }
          load();
        });
        b.dataset.tierCaller = harness;
        b.setAttribute("aria-pressed", String(harness === view.harness));
        seg.append(b);
      });
      callerBox.append(seg);
      callerBox.title = tr("js.settings.tier_routing.caller_hint", view.harness);
      const legend = el("div", "tr-legend");
      [["default", "js.settings.tier_routing.legend.default"], ["user", "js.settings.tier_routing.legend.user"], ["exc", "js.settings.tier_routing.legend.exception"], ["bundle", "js.settings.tier_routing.legend.bundle"], ["fall", "js.settings.tier_routing.legend.caller"]].forEach(function (pair) {
        const item = el("span", "tr-lg " + pair[0]);
        item.append(el("i"), el("span", "", tr(pair[1])));
        legend.append(item);
      });
      bar.append(callerBox, legend);
      return bar;
    }

    function nodeCard(view, node) {
      const card = btn("tr-node kind-" + node.kind + (node.overrides ? " over" : "") + (state.focus === node.name ? " focus" : ""), "", function () { openNode(node.name); });
      card.dataset.id = "n:" + node.name;
      card.dataset.tierNode = node.name;
      const top = el("span", "tr-nh");
      top.append(el("span", "tr-dot"), el("b", "tr-name", node.name));
      // Playbook roles are most of the graph: their size says what they are.
      if (node.kind !== "role") top.append(el("span", "tr-badge", tr(KIND_KEY[node.kind] || KIND_KEY.user)));
      if (node.size) top.append(el("span", "tr-size" + (node.kind === "role" ? " solo" : ""), SIZE_SHORT[node.size] || node.size));
      card.append(top);
      const main = node.pointers["*"];
      if (main) {
        card.append(el("span", "tr-ptr", "→ " + main));
      } else {
        const fall = el("span", "tr-ptr fall", tr("js.settings.tier_routing.fall", view.harness));
        fall.dataset.tierFallthrough = view.harness;
        card.append(fall);
      }
      view.sizes.forEach(function (size) {
        if (node.pointers[size]) card.append(el("span", "tr-exc", ":" + size + " → " + node.pointers[size]));
      });
      // The card's own route ends on a harness agent.spawn cannot launch.
      const own = routeFor(node);
      if (own && own.unavailable) {
        card.classList.add("unavail");
        card.dataset.tierUnavailable = own.harness;
        top.append(unavailMark(own.unavailable));
      }
      card.addEventListener("mouseenter", function () { setFocus(node.name); });
      card.addEventListener("focus", function () { setFocus(node.name); });
      return card;
    }

    function unavailMark(why) {
      const mark = el("span", "tr-unavail-mark", "!");
      mark.title = why;
      mark.setAttribute("aria-label", why);
      return mark;
    }

    function terminalCard(view, term) {
      const inbound = view.nodes.reduce(function (sum, n) {
        return sum + n.edges.filter(function (e) { return edgeTargetId(e) === term.id; }).length;
      }, 0);
      const card = term.kind === "bundle" ? btn("tr-term bundle", "", function () { openBundle(term.name); }) : el("div", "tr-term " + term.kind);
      card.dataset.id = term.id;
      const top = el("span", "tr-nh");
      if (term.kind === "caller") {
        card.dataset.tierCallerTerminal = term.name;
        top.append(el("b", "tr-name", tr("js.settings.tier_routing.caller.title", term.name)), el("span", "tr-badge fall", tr("js.settings.tier_routing.legend.caller")));
      } else if (term.kind === "bundle") {
        top.append(el("b", "tr-name", term.name), el("span", "tr-badge bundle", tr("js.settings.tier_routing.kind.bundle")));
      } else {
        top.append(el("b", "tr-name", term.name + ".*"));
      }
      top.append(el("span", "tr-count", String(inbound)));
      card.append(top);
      // The daemon's availability check for the terminal's harness.
      const harness = term.kind === "bundle" ? term.bundle.harness : term.name;
      const why = view.unavailable && view.unavailable[harness];
      const unknown = view.availability_unknown && view.availability_unknown[harness];
      if (why) {
        card.classList.add("unavail");
        const line = el("span", "tr-ptr tr-unavail", why);
        line.dataset.tierUnavailable = harness;
        card.append(line);
      } else if (unknown) {
        // Not checked yet: the daemon never waits on a probe for the view.
        const line = el("span", "tr-ptr tr-unknown", unknown);
        line.dataset.tierAvailabilityUnknown = harness;
        card.append(line);
      }
      if (term.kind === "bundle") {
        const b = term.bundle;
        card.append(el("span", "tr-ptr mono", [b.harness, b.provider, b.model, b.effort].filter(Boolean).join(" | ")));
      } else {
        if (term.kind === "caller") card.append(el("span", "tr-ptr", tr("js.settings.tier_routing.caller.sub", term.name)));
        const chips = el("span", "tr-chips");
        view.sizes.forEach(function (size) { chips.append(el("span", "tr-chip", SIZE_SHORT[size])); });
        card.append(chips);
      }
      return card;
    }

    function drawEdges(view, frame, svg) {
      if (!frame.isConnected) return;
      const box = frame.getBoundingClientRect();
      svg.setAttribute("width", String(frame.scrollWidth));
      svg.setAttribute("height", String(frame.scrollHeight));
      svg.replaceChildren();
      const at = {};
      frame.querySelectorAll("[data-id]").forEach(function (node) { at[node.dataset.id] = node.getBoundingClientRect(); });
      const focus = findNode(state.focus), lit = {};
      if (focus) {
        const route = routeFor(focus);
        lit[focus.name] = true;
        (route && route.hops || []).forEach(function (hop) { lit[hop.from.split(":")[0]] = true; });
      }
      view.nodes.forEach(function (node) {
        const from = at["n:" + node.name];
        if (!from) return;
        node.edges.forEach(function (edge) {
          const to = at[edgeTargetId(edge)];
          if (!to) return;
          const x1 = from.right - box.left, y1 = from.top + from.height / 2 - box.top;
          const x2 = to.left - box.left, y2 = to.top + to.height / 2 - box.top;
          const dx = Math.max(24, (x2 - x1) / 2);
          const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
          path.setAttribute("d", "M" + x1 + "," + y1 + " C" + (x1 + dx) + "," + y1 + " " + (x2 - dx) + "," + y2 + " " + x2 + "," + y2);
          path.setAttribute("class", "tr-edge " + edgeClass(node, edge) + (lit[node.name] ? " lit" : ""));
          svg.append(path);
          if (edge.selector !== "*") {
            const label = document.createElementNS("http://www.w3.org/2000/svg", "text");
            label.setAttribute("x", String((x1 + x2) / 2));
            label.setAttribute("y", String((y1 + y2) / 2 - 3));
            label.setAttribute("class", "tr-edge-label");
            label.textContent = ":" + edge.selector;
            svg.append(label);
          }
        });
      });
    }

    function routeFor(node) {
      const size = node.size || "large";
      return node.routes.find(function (r) { return r.size === size; });
    }

    function setFocus(name) {
      if (state.focus === name) return;
      state.focus = name;
      const frame = body.querySelector(".tr-frame");
      if (!frame) return;
      frame.querySelectorAll(".tr-node.focus").forEach(function (n) { n.classList.remove("focus"); });
      const card = Array.from(frame.querySelectorAll(".tr-node")).find(function (n) { return n.dataset.tierNode === name; });
      if (card) card.classList.add("focus");
      drawEdges(state.view, frame, frame.querySelector(".tr-edges"));
      const bar = body.querySelector(".tr-trace");
      if (bar) bar.replaceWith(traceBar(state.view));
    }

    function traceBar(view) {
      const bar = el("div", "tr-trace");
      bar.append(el("span", "tr-trace-lbl", tr("js.settings.tier_routing.trace_caller", view.harness)));
      const node = findNode(state.focus);
      const route = node && routeFor(node);
      bar.append(routeChips(route, tr("js.settings.tier_routing.trace_none")));
      return bar;
    }

    function routeChips(route, empty) {
      const wrap = el("span", "tr-route");
      if (!route) {
        wrap.append(el("span", "hint", empty));
        return wrap;
      }
      if (route.error) {
        wrap.append(el("span", "tr-err", route.error));
        return wrap;
      }
      wrap.append(el("code", "tr-hop", route.token));
      (route.hops || []).forEach(function (hop, i, hops) {
        wrap.append(el("span", "tr-arrow", "→"));
        const last = i === hops.length - 1;
        let text = hop.to;
        if (hop.via === "fallthrough") text += " · " + tr("js.settings.tier_routing.trace.bare");
        if (hop.via === "caller") text += " · " + tr("js.settings.tier_routing.trace.caller");
        if (hop.via === "alias") text += " · " + tr("js.settings.tier_routing.trace.alias") + " " + hop.from;
        const chip = el("code", "tr-hop" + (hop.via === "caller" || hop.via === "fallthrough" ? " fall" : "") + (last ? " end" : ""), text);
        chip.dataset.via = hop.via;
        wrap.append(chip);
      });
      if (route.bundle && !(route.hops || []).length) wrap.lastChild.classList.add("end");
      const binding = route.binding_error ? el("span", "tr-err", route.binding_error) :
        el("span", "tr-binding", [route.harness, route.provider, route.model, route.effort].filter(Boolean).join(" · "));
      if (route.unavailable) binding.dataset.tierUnavailable = route.harness;
      wrap.append(binding);
      if (route.availability_unknown) {
        const note = el("span", "tr-unknown", route.availability_unknown);
        note.dataset.tierAvailabilityUnknown = route.harness;
        wrap.append(note);
      }
      return wrap;
    }

    // --- editing ----------------------------------------------------------

    function targetSelect(view, self, allowNone, current) {
      const sel = el("select", "sel mono tr-target");
      if (allowNone) sel.append(option("none:", tr("js.settings.tier_routing.none", view.harness)));
      const groups = [
        ["nodes", view.nodes.filter(function (n) { return n.name !== self; }).map(function (n) { return ["node:" + n.name, n.name]; })],
        ["harnesses", view.harnesses.map(function (h) { return ["harness:" + h, h]; })],
        ["bundles", view.bundles.map(function (b) { return ["bundle:" + b.name, b.name]; })]
      ];
      groups.forEach(function (group) {
        if (!group[1].length) return;
        const og = el("optgroup");
        og.label = tr(GROUP_KEY[group[0]]);
        group[1].forEach(function (pair) { og.append(option(pair[0], pair[1])); });
        sel.append(og);
      });
      const want = current.kind === "none" ? "none:" : current.kind + ":" + current.name;
      if (!Array.from(sel.options).some(function (o) { return o.value === want; })) sel.append(option(want, encode(current) || want));
      sel.value = want;
      return sel;
    }

    function sizeSelect(view, current) {
      const sel = el("select", "sel mono tr-sizemode");
      sel.append(option("*", tr("js.settings.tier_routing.size.pass")));
      view.sizes.forEach(function (size) { sel.append(option(size, size)); });
      sel.value = current && current !== "" ? current : "*";
      return sel;
    }

    // pointerRow edits one pointer: a target and, for a node or harness
    // slot, whether it passes the requested size (*) or pins one.
    function pointerRow(view, self, allowNone, pointer, changed) {
      const current = decode(pointer);
      const row = el("div", "tr-prow");
      const target = targetSelect(view, self, allowNone, current);
      const size = sizeSelect(view, current.size);
      function sync() {
        const kind = target.value.split(":")[0];
        size.disabled = kind === "none" || kind === "bundle";
      }
      target.addEventListener("change", function () { sync(); changed(); });
      size.addEventListener("change", changed);
      sync();
      row.append(target, size);
      return {
        node: row, target: target, size: size,
        read: function () {
          const i = target.value.indexOf(":");
          const kind = target.value.slice(0, i), name = target.value.slice(i + 1);
          return encode({ kind: kind, name: name, size: size.value });
        }
      };
    }

    function modal(title, badge) {
      const dialog = el("dialog", "tr-modal");
      dialog.setAttribute("aria-label", title);
      const hd = el("div", "tr-modal-hd");
      const heading = el("h2", "", title);
      hd.append(heading);
      if (badge) hd.append(el("span", "tr-badge", badge));
      const close = btn("tr-modal-x", "×", function () { dialog.close(); });
      close.setAttribute("aria-label", tr("js.settings.tier_routing.cancel"));
      hd.append(close);
      const content = el("div", "tr-modal-body");
      const error = el("div", "tr-modal-err");
      error.setAttribute("role", "alert");
      error.hidden = true;
      const foot = el("div", "tr-modal-foot");
      dialog.append(hd, content, error, foot);
      dialog.addEventListener("close", function () { dialog.remove(); });
      document.body.append(dialog);
      return { dialog: dialog, content: content, error: error, foot: foot,
        fail: function (text) { error.textContent = text || ""; error.hidden = !text; } };
    }

    function field(label, control, hint) {
      const wrap = el("label", "tr-field");
      wrap.append(el("span", "tr-flbl", label), control);
      if (hint) wrap.append(el("span", "hint", hint));
      return wrap;
    }

    // previewer debounces daemon previews of draft(); show(view|null, why)
    // receives the daemon's description, or why the draft is not ready.
    // Every edit bumps seq, so a response to an older draft is dropped even
    // when the newer draft is still debouncing or never reaches the daemon.
    function previewer(draft, show) {
      let timer = 0, seq = 0;
      return function () {
        const mine = ++seq;
        clearTimeout(timer);
        timer = setTimeout(async function () {
          const built = draft();
          if (built.problem) { show(null, built.problem, built); return; }
          const result = await call("/api/config/tier-routing/preview", { harness: state.harness, value: built.value });
          if (mine !== seq) return;
          if (!result.ok) { show(null, result.error || tr("js.settings.error." + result.code), built); return; }
          show(result.json, "", built);
        }, 120);
      };
    }

    function openNode(name) {
      const view = state.view;
      const isNew = !name;
      const node = isNew ? { name: "", kind: "user", size: "", pointers: {}, defaults: {}, overrides: {}, inbound: [] } : findNode(name);
      if (!node) return;
      const stored = compact(routingOf(view));
      const m = modal(isNew ? tr("js.settings.tier_routing.modal.new_node") : tr("js.settings.tier_routing.modal.node", node.name), tr(KIND_KEY[node.kind] || KIND_KEY.user));
      m.dialog.dataset.tierModal = "node";
      const readOnly = !ctx.editable;
      let nameInput = null;
      if (isNew) {
        nameInput = el("input", "inp mono tr-name-input");
        nameInput.type = "text";
        nameInput.setAttribute("aria-label", tr("js.settings.tier_routing.name"));
        nameInput.addEventListener("input", changed);
        m.content.append(field(tr("js.settings.tier_routing.name"), nameInput, tr("js.settings.tier_routing.name.hint")));
      }
      if (node.defaults["*"]) m.content.append(el("p", "hint", tr("js.settings.tier_routing.default_was", node.defaults["*"])));
      const primary = pointerRow(view, node.name, node.kind !== "role", node.pointers["*"] || "", changed);
      m.content.append(field(tr("js.settings.tier_routing.target"), primary.node));
      const excBox = el("div", "tr-exceptions");
      const excHead = el("div", "tr-exc-hd");
      excHead.append(el("b", "", tr("js.settings.tier_routing.exceptions")));
      const excList = el("div", "tr-exc-list");
      const excNone = el("p", "hint", tr("js.settings.tier_routing.exceptions.none"));
      const exceptions = [];
      function addException(size, pointer) {
        const row = el("div", "tr-exc-row");
        const sizeSel = el("select", "sel mono tr-exc-size");
        view.sizes.forEach(function (s) { sizeSel.append(option(s, ":" + s)); });
        sizeSel.value = size;
        sizeSel.addEventListener("change", changed);
        const ptr = pointerRow(view, node.name, false, pointer, changed);
        const entry = { size: sizeSel, ptr: ptr, row: row };
        const remove = btn("btn tr-exc-remove", tr("js.settings.tier_routing.remove"), function () {
          exceptions.splice(exceptions.indexOf(entry), 1);
          row.remove();
          excNone.hidden = exceptions.length > 0;
          changed();
        });
        row.append(sizeSel, ptr.node, remove);
        excList.append(row);
        exceptions.push(entry);
        excNone.hidden = true;
      }
      const addExc = btn("btn tr-exc-add", tr("js.settings.tier_routing.add_exception"), function () {
        const used = exceptions.map(function (e) { return e.size.value; });
        const free = view.sizes.find(function (s) { return used.indexOf(s) < 0; });
        if (!free) return;
        addException(free, node.pointers["*"] || (view.harnesses[0] + ".*"));
        changed();
      });
      excHead.append(addExc);
      excBox.append(excHead, excNone, excList);
      view.sizes.forEach(function (size) { if (node.pointers[size]) addException(size, node.pointers[size]); });
      excNone.hidden = exceptions.length > 0;
      m.content.append(excBox);
      if (node.inbound.length) m.content.append(el("p", "hint tr-inbound", tr("js.settings.tier_routing.inbound", refsText(node.inbound))));
      const traceBox = el("div", "tr-modal-trace");
      traceBox.append(el("b", "", tr("js.settings.tier_routing.live_trace", view.harness)));
      const traceRows = el("div", "tr-trace-rows");
      traceBox.append(traceRows);
      m.content.append(traceBox);

      const reset = !isNew && node.kind !== "user" && node.overrides ? btn("btn tr-reset-node", tr("js.settings.tier_routing.reset_node"), function () {
        const routing = routingOf(view);
        delete routing.nodes[node.name];
        commit(compact(routing));
      }) : null;
      const del = !isNew && node.kind === "user" ? btn("btn danger tr-delete", tr("js.settings.tier_routing.delete"), function () {
        if (node.inbound.length) { m.fail(tr("js.settings.tier_routing.refused_delete", refsText(node.inbound))); return; }
        const routing = routingOf(view);
        delete routing.nodes[node.name];
        commit(compact(routing));
      }) : null;
      const cancel = btn("btn", tr("js.settings.tier_routing.cancel"), function () { m.dialog.close(); });
      const saveBtn = btn("btn pri tr-save", tr("js.settings.tier_routing.save"), function () { if (pending) commit(pending.value); });
      saveBtn.disabled = true;
      if (readOnly) {
        m.foot.append(el("span", "hint", tr("js.settings.tier_routing.read_only")), cancel);
        m.content.querySelectorAll("select, input, button").forEach(function (c) { c.disabled = true; });
      } else {
        if (reset) m.foot.append(reset);
        if (del) m.foot.append(del);
        m.foot.append(el("span", "tr-sp"), cancel, saveBtn);
      }
      let pending = null;

      function build() {
        const nodeName = isNew ? nameInput.value.trim() : node.name;
        if (!nodeName) return { problem: tr("js.settings.tier_routing.name_required") };
        if (isNew && (findNode(nodeName) || findBundle(nodeName))) return { problem: tr("js.settings.tier_routing.name_taken", nodeName) };
        const wanted = { "*": primary.read() };
        const sizes = {};
        for (const e of exceptions) {
          if (sizes[e.size.value]) return { problem: tr("js.settings.tier_routing.dup_size") };
          sizes[e.size.value] = true;
          wanted[e.size.value] = e.ptr.read();
        }
        const routing = routingOf(view);
        const builtin = node.kind !== "user";
        const own = {};
        Object.keys(wanted).forEach(function (selector) {
          const pointer = wanted[selector];
          if (!pointer) return;
          if (builtin && node.defaults[selector] === pointer) return;
          own[selector] = pointer;
        });
        if (builtin && !Object.keys(own).length) delete routing.nodes[nodeName];
        else routing.nodes[nodeName] = own;
        return { value: compact(routing), name: nodeName };
      }
      const preview = previewer(build, function (result, problem, built) {
        traceRows.replaceChildren();
        pending = null;
        m.fail(problem || (result && result.error) || (m.stale ? state.notice : ""));
        // An invalid draft has no traces: the error box says why once.
        const subject = result && !result.error && result.nodes.find(function (n) { return n.name === built.name; });
        if (subject) {
          subject.routes.forEach(function (route) {
            const row = el("div", "tr-trace-row");
            row.append(el("span", "tr-trace-size", route.size + ":"), routeChips(route, ""));
            traceRows.append(row);
          });
        }
        const unchanged = built && built.value && same(built.value, stored);
        if (result && !result.error && !unchanged) pending = built;
        saveBtn.disabled = readOnly || m.stale || !pending;
      });
      function changed() { pending = null; saveBtn.disabled = true; preview(); }
      async function commit(value) {
        saveBtn.disabled = true;
        const saved = await save(value, view.revision, m);
        if (saved) m.dialog.close();
        else saveBtn.disabled = m.stale || !pending;
      }
      m.dialog.showModal();
      preview();
      if (nameInput) nameInput.focus();
    }

    function openBundle(name) {
      const view = state.view;
      const isNew = !name;
      const bundle = isNew ? { name: "", harness: view.harness, provider: "", model: "", effort: "", inbound: [] } : findBundle(name);
      if (!bundle) return;
      const stored = compact(routingOf(view));
      const m = modal(isNew ? tr("js.settings.tier_routing.modal.new_bundle") : tr("js.settings.tier_routing.modal.bundle", bundle.name), tr("js.settings.tier_routing.kind.bundle"));
      m.dialog.dataset.tierModal = "bundle";
      const readOnly = !ctx.editable;
      let nameInput = null;
      if (isNew) {
        nameInput = el("input", "inp mono tr-name-input");
        nameInput.type = "text";
        nameInput.setAttribute("aria-label", tr("js.settings.tier_routing.name"));
        nameInput.addEventListener("input", changed);
        m.content.append(field(tr("js.settings.tier_routing.name"), nameInput, tr("js.settings.tier_routing.name.hint")));
      }
      const cb = typeof window !== "undefined" ? window.WsSettingsCombobox : null;
      const harness = el("select", "sel mono tr-bundle-harness");
      view.harnesses.forEach(function (h) { harness.append(option(h, h)); });
      harness.value = bundle.harness;
      let prevHarness = harness.value;
      const provider = el("select", "sel mono tr-bundle-provider");
      provider.disabled = readOnly;
      provider.setAttribute("aria-label", tr("js.settings.tier_routing.provider"));
      function ensureProviderOption(value) {
        if (!value) return;
        if (!Array.from(provider.options).some(function (o) { return o.value === value; })) {
          provider.append(option(value, value));
        }
      }
      function populateProviders(selected) {
        const current = selected !== undefined ? selected : provider.value;
        provider.replaceChildren();
        provider.append(option("", tr("js.settings.tier_routing.provider.default")));
        const list = cb && typeof cb.providerOptions === "function" ? cb.providerOptions("werk") : ["openrouter", "openai"];
        list.forEach(function (p) { provider.append(option(p, p)); });
        ensureProviderOption(current);
        provider.value = current || "";
      }
      populateProviders(harness.value === "werk" ? (bundle.provider || "") : "");
      const providerField = field(tr("js.settings.tier_routing.provider"), provider);
      providerField.hidden = harness.value !== "werk";
      const model = el("input", "inp mono tr-bundle-model");
      model.type = "text";
      model.value = harness.value === "werk" && cb ? cb.modelKey(bundle.model, bundle.provider) : bundle.model;
      model.disabled = readOnly;
      model.setAttribute("aria-label", tr("js.settings.tier_routing.model"));
      model.addEventListener("input", function () {
        if (harness.value === "werk" && cb) {
          const picked = cb.modelSelection(model.value.trim(), "");
          if (picked.provider) {
            ensureProviderOption(picked.provider);
            provider.value = picked.provider;
          }
        }
        changed();
      });
      provider.addEventListener("change", function () {
        if (harness.value === "werk" && cb) {
          const bare = cb.modelSelection(model.value.trim(), "").model;
          if (bare) model.value = cb.modelKey(bare, provider.value);
        }
        changed();
      });
      const effort = el("input", "inp mono tr-bundle-effort");
      effort.type = "text";
      effort.value = bundle.effort || "";
      effort.disabled = readOnly;
      effort.setAttribute("aria-label", tr("js.settings.tier_routing.effort"));
      effort.addEventListener("input", changed);
      const modelCtrl = cb ? cb.attach(model, { getOptions: function () { return cb.modelOptions(harness.value); } }) : null;
      const effortCtrl = cb ? cb.attach(effort, { getOptions: function () { return cb.effortOptions(harness.value); } }) : null;
      if (cb && typeof cb.loadChoices === "function") {
        cb.loadChoices().then(function () {
          if (m.dialog.isConnected && harness.value === "werk") populateProviders(provider.value);
        });
      }
      harness.addEventListener("change", function () {
        const nextHarness = harness.value;
        providerField.hidden = nextHarness !== "werk";
        if (nextHarness === "werk") {
          populateProviders(provider.value);
        } else {
          if (prevHarness === "werk" && cb) {
            model.value = cb.modelSelection(model.value.trim(), "").model;
          }
          provider.value = "";
        }
        prevHarness = nextHarness;
        if (modelCtrl) modelCtrl.refresh();
        if (effortCtrl) effortCtrl.refresh();
        changed();
      });
      m.content.append(
        field(tr("js.settings.tier_routing.harness"), harness),
        providerField,
        field(tr("js.settings.tier_routing.model"), modelCtrl ? modelCtrl.node : model),
        field(tr("js.settings.tier_routing.effort"), effortCtrl ? effortCtrl.node : effort)
      );
      if (bundle.inbound.length) m.content.append(el("p", "hint tr-inbound", tr("js.settings.tier_routing.inbound", refsText(bundle.inbound))));
      const traceBox = el("div", "tr-modal-trace");
      traceBox.append(el("b", "", tr("js.settings.tier_routing.live_trace", view.harness)));
      const traceRows = el("div", "tr-trace-rows");
      traceBox.append(traceRows);
      m.content.append(traceBox);
      const del = !isNew ? btn("btn danger tr-delete", tr("js.settings.tier_routing.delete"), function () {
        if (bundle.inbound.length) { m.fail(tr("js.settings.tier_routing.refused_delete", refsText(bundle.inbound))); return; }
        const routing = routingOf(view);
        delete routing.bundles[bundle.name];
        commit(compact(routing));
      }) : null;
      const cancel = btn("btn", tr("js.settings.tier_routing.cancel"), function () { m.dialog.close(); });
      const saveBtn = btn("btn pri tr-save", tr("js.settings.tier_routing.save"), function () { if (pending) commit(pending.value); });
      saveBtn.disabled = true;
      if (readOnly) {
        m.foot.append(el("span", "hint", tr("js.settings.tier_routing.read_only")), cancel);
        m.content.querySelectorAll("select, input, button").forEach(function (c) { c.disabled = true; });
      } else {
        if (del) m.foot.append(del);
        m.foot.append(el("span", "tr-sp"), cancel, saveBtn);
      }
      let pending = null;
      function build() {
        const bundleName = isNew ? nameInput.value.trim() : bundle.name;
        if (!bundleName) return { problem: tr("js.settings.tier_routing.name_required") };
        if (isNew && (findNode(bundleName) || findBundle(bundleName))) return { problem: tr("js.settings.tier_routing.name_taken", bundleName) };
        const routing = routingOf(view);
        let modelValue = model.value.trim();
        let providerValue = "";
        if (harness.value === "werk") {
          const picked = cb ? cb.modelSelection(modelValue, provider.value) : { model: modelValue, provider: provider.value };
          modelValue = picked.model;
          providerValue = picked.provider;
        }
        const value = { harness: harness.value, model: modelValue };
        if (providerValue) value.provider = providerValue;
        if (effort.value.trim()) value.effort = effort.value.trim();
        routing.bundles[bundleName] = value;
        return { value: compact(routing), name: bundleName };
      }
      const preview = previewer(build, function (result, problem, built) {
        traceRows.replaceChildren();
        pending = null;
        m.fail(problem || (result && result.error) || (m.stale ? state.notice : ""));
        const subject = result && !result.error && result.bundles.find(function (b) { return b.name === built.name; });
        if (subject) traceRows.append(routeChips(subject.route, ""));
        const unchanged = built && built.value && same(built.value, stored);
        if (result && !result.error && !unchanged) pending = built;
        saveBtn.disabled = readOnly || m.stale || !pending;
      });
      function changed() { pending = null; saveBtn.disabled = true; preview(); }
      async function commit(value) {
        saveBtn.disabled = true;
        const saved = await save(value, view.revision, m);
        if (saved) m.dialog.close();
        else saveBtn.disabled = m.stale || !pending;
      }
      m.dialog.showModal();
      preview();
      if (nameInput) nameInput.focus();
    }

    // save stores draft as the whole graph (an empty draft resets the key
    // so the packaged defaults apply) and reloads the daemon's view. base is
    // the revision of the view the draft was built from. A refusal shows the
    // daemon's reason in the modal and keeps the stored graph in force; a
    // stale base (the graph changed since) also reloads the graph, keeps the
    // reason on the card, and locks the modal's writes, since its draft was
    // built from the old graph.
    async function save(draft, base, m) {
      const empty = !Object.keys(draft).length;
      const request = { scope: "global", target: "", context: { worktree: "", session: "" }, key: ctx.entry.key, selectors: {}, revision: base || "" };
      if (!empty) request.value = draft;
      const result = await call("/api/config/" + (empty ? "reset" : "set"), request);
      if (!result.ok) {
        const why = result.error || tr("js.settings.error." + result.code);
        if (m) m.fail(why);
        else { status.className = "tr-status bad"; status.textContent = why; }
        if (result.code === "stale_revision") {
          state.notice = why;
          if (m) {
            m.stale = true;
            m.foot.querySelectorAll(".tr-save, .tr-delete, .tr-reset-node").forEach(function (b) { b.disabled = true; });
          }
          await load();
        }
        return false;
      }
      state.notice = "";
      if (ctx.onSaved) ctx.onSaved(result.json);
      await load();
      return true;
    }
  }

  window.WsSettingsTierRouting = { render: render };
})();
