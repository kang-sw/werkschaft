  // --- stream ----------------------------------------------------------------
  const consentBanner = doc.querySelector("[data-openai-consent]");
  let consentPrompt = 0, consentLocal = false, dismissedConsentPrompt = 0;
  let consentResize;

  function setConsentBannerHidden(hidden) {
    consentBanner.hidden = hidden;
    if (hidden) {
      root.removeAttribute("data-account-banner");
      consentResize?.disconnect();
      root.style.removeProperty("--account-banner-height");
    } else {
      root.setAttribute("data-account-banner", "");
      if (typeof ResizeObserver === "function") {
        consentResize ||= new ResizeObserver(([entry]) => {
          if (!consentBanner.hidden) root.style.setProperty("--account-banner-height", entry.borderBoxSize[0].blockSize + "px");
        });
        consentResize.observe(consentBanner, {box: "border-box"});
      }
    }
  }

  function showOpenAIConsent(event) {
    if (!consentBanner || !event || !Number.isSafeInteger(event.prompt) || typeof event.local !== "boolean") return;
    const {prompt, local} = event;
    if (prompt === consentPrompt && local === consentLocal) return;
    consentPrompt = prompt;
    consentLocal = local;
    setConsentBannerHidden(!prompt || prompt === dismissedConsentPrompt);
    consentBanner.replaceChildren();
    doc.dispatchEvent(new CustomEvent("ws-openai-consent", {detail: event}));
    if (consentBanner.hidden) return;

    function item(tag, className, key) {
      const node = doc.createElement(tag);
      node.className = className;
      node.textContent = wsT(key);
      return node;
    }
    const copy = doc.createElement("div");
    copy.className = "account-banner-copy";
    copy.append(item("strong", "", "js.account.banner_title"), item("p", "", "js.account.banner_description"));
    consentBanner.append(copy);
    if (!local) {
      copy.append(item("p", "account-banner-local", "js.account.banner_local_hint"));
      return;
    }
    const link = item("a", "account-link", "js.account.banner_openai_link");
    link.href = "/settings?section=account&provider=openai";
    copy.append(link);
    const controls = doc.createElement("div");
    controls.className = "account-banner-controls";
    consentBanner.append(controls);
    const error = item("p", "account-banner-error", "js.account.banner_failed");
    error.hidden = true;
    error.setAttribute("role", "alert");
    copy.append(error);

    function action(key, allowed, primary) {
      const node = item("button", "btn" + (primary ? " pri" : ""), key);
      node.type = "button";
      node.addEventListener("click", async function () {
        const buttons = controls.querySelectorAll("button");
        buttons.forEach(button => { button.disabled = true; });
        error.hidden = true;
        const dismiss = allowed === undefined;
        try {
          const response = await fetch("/api/account/consent" + (dismiss ? "/dismiss" : ""), {
            method: "POST", credentials: "same-origin", headers: {"Content-Type": "application/json"},
            body: JSON.stringify(dismiss ? {prompt} : {prompt, allowed})
          });
          if (!response.ok) throw new Error();
          dismissedConsentPrompt = prompt;
          if (consentPrompt === prompt) setConsentBannerHidden(true);
        } catch (_) {
          error.hidden = false;
        } finally {
          buttons.forEach(button => { button.disabled = false; });
        }
      });
      return node;
    }
    const close = action("js.account.banner_close");
    close.className = "btn account-banner-close";
    close.title = close.textContent;
    close.setAttribute("aria-label", close.textContent);
    close.textContent = "×";
    controls.append(action("js.account.banner_allow", true, true), action("js.account.banner_deny", false), close);
  }

  // The stream state shows on every indicator in the top bar: the bare dot,
  // and the harness badge's LED, which a head region swap redraws lit, so
  // the swap reapplies the state (swap above).
  // --- tab label: the count of work waiting on the user ---------------------
  // This page owns document.title (the html element's data-wb-titlehost tells
  // the board's open-question pill script to report its count here instead
  // of writing the title). The label is "(N) <base>", no prefix at 0, where N
  // sums three sources, each cached where it changes so a recount is a sum:
  //   - the rendered tracker store's unread notifications (tlTk, from the
  //     renderer's onRender);
  //   - board asks: on a session page the pill's open set (tlPill), on the
  //     index the sessions' policy chips;
  //   - permission prompts: one per agent (or peer) in the tree that shows
  //     one, the conversation's wait folded into the lead; on the index the
  //     sessions' approval phases.
  // N drops only when an item is handled (read, answered, resolved), since
  // every source is recounted, never decremented by focus. A hidden tab holds
  // region updates back (connect), so a held update of a counted region is
  // parsed detached instead, once per new hash (tlHeld), and the counts read
  // it in place of the live region until the tab shows again.
  var TL_REGIONS = mode === "index" ? { sessions: 1 } : { board: 1, convlive: 1, tree: 1 };
  var TL_ASK = '.card.policy[id^="item-"], .arow[id^="item-"], .ckrow[data-id]:not([data-assumed])';
  var tlHeld = {}, tlPill = 0, tlTk = 0, tlAsks = -1, tlPerm = 0, tlN = -1, tlBase = null;
  function tlRetitle() {
    // The shell's title is its shown frame's (the title message).
    if (shell) return;
    var n = tlTk + tlPerm + (tlAsks >= 0 ? tlAsks : tlPill);
    if (n === tlN) return;
    tlN = n;
    if (tlBase === null) {
      tlBase = root.getAttribute("data-wb-title");
      if (tlBase === null) { tlBase = doc.title.replace(/^\(\d+\) /, ""); root.setAttribute("data-wb-title", tlBase); }
    }
    doc.title = (n ? "(" + n + ") " : "") + tlBase;
    shPost("title", { title: doc.title });
  }
  window.wbTitleCount = function (n) { tlPill = n; tlRetitle(); };
  // tlRebase replaces the label's base (a rename's new tab title, which a
  // head swap brings).
  function tlRebase(base) {
    if (shell || !base || base === tlBase) return;
    tlBase = base;
    root.setAttribute("data-wb-title", base);
    tlN = -1;
    tlRetitle();
  }
  function tlRoot(name) { return tlHeld[name] ? tlHeld[name].root : region(name); }
  // tlDrafted: the pill's reply rule (a pick, a ticked ack, or a note).
  function tlDrafted(el) {
    var box = el.querySelector("label.ack > input, label.ck > input"), n = el.querySelector("textarea.note");
    return !!(el.querySelector('.opt[data-opt][aria-pressed="true"]') || (box && box.checked) || el.hasAttribute("data-no") || (n && n.value.trim()));
  }
  // tlHeldAsks counts a held board the pill's way without its DOM: its asks,
  // less those drafted in the live board and those the newest sent records
  // (in the held conversation when one waits, else the live one) name.
  function tlHeldAsks() {
    var b = tlRoot("board"), live = region("board"), c = tlRoot("convlive"), drafted = {}, sent = {};
    if (!b) return 0;
    if (live) boardQ(TL_ASK).forEach(function (el) { if (tlDrafted(el)) drafted[el.getAttribute("data-id")] = 1; });
    if (c) q(".sentdata i[data-id]", c).forEach(function (i) { sent[i.getAttribute("data-id")] = 1; });
    return q(TL_ASK, b).filter(function (el) {
      var id = el.getAttribute("data-id");
      return !drafted[id] && !sent[id] && !(window.wbPill && window.wbPill.isHeld && window.wbPill.isHeld(el));
    }).length;
  }
  // The tree's hidden compact roster (.ag-source) does not count: it repeats
  // this tree's running rows outside any .agent and adds linked delegate
  // sessions' agents, so it would add a lead wait that is not there.
  function tlPerms() {
    var t = tlRoot("tree"), c = tlRoot("convlive"), units = [];
    function add(u) { if (units.indexOf(u) < 0) units.push(u); }
    if (t) q(".phase.permission, .brow.perm", t).forEach(function (el) { if (!el.closest(".ag-source")) add(el.closest(".agent, .peer") || "lead"); });
    if (c && c.querySelector(".permwait")) add((t && t.querySelector(".agent.root")) || "lead");
    return units.length;
  }
  // tlCount recounts the region-borne sources from the live regions, or the
  // held parse of one.
  function tlCount() {
    if (mode === "index") {
      var r = tlRoot("sessions");
      tlAsks = r ? q(".sess .chip.policy", r).reduce(function (a, el) { return a + (parseInt(el.textContent, 10) || 0); }, 0) : 0;
      tlPerm = r ? q(".sess .phase.permission", r).length : 0;
    } else {
      tlAsks = tlHeld.board || tlHeld.convlive ? tlHeldAsks() : -1;
      tlPerm = tlPerms();
    }
    tlRetitle();
  }
  // tlHold parses a held update of a counted region, once per new hash.
  function tlHold(name, hash, html) {
    if (!TL_REGIONS[name]) return;
    var was = tlHeld[name], el = region(name);
    if (was ? was.hash === hash : !el || el.getAttribute("data-h") === hash) return;
    tlHeld[name] = { hash: hash, root: name === "board" && typeof html !== "string" ? boardInputFragment(html) : frag(name === "tree" ? treeInputHTML(html) : html) };
    tlCount();
  }
  // The tracker term and its new arrivals: each store's first loaded render
  // sets its baseline of unread keys silently; a later key not in it is new.
  // A new one toasts while the tab shows, and waits in tlHidden for one
  // summary toast while it is hidden (tlShown).
  var tlBaseKeys = {}, tlHidden = [], tlLastU = null;
  function tlTracker(u) {
    tlLastU = u;
    // a store no longer listed forgets its baseline and waiting arrivals;
    // listed again, it sets a new baseline silently
    Object.keys(tlBaseKeys).forEach(function (s) { if (tkOrder.indexOf(s) < 0) delete tlBaseKeys[s]; });
    tlHidden = tlHidden.filter(function (x) { return tkOrder.indexOf(x.store) >= 0; });
    tlTk = u && u.ready ? u.entries.length : 0;
    if (u && u.ready) {
      var base = tlBaseKeys[u.store];
      if (!base) {
        base = tlBaseKeys[u.store] = {};
        u.entries.forEach(function (x) { base[x.key] = 1; });
      } else {
        u.entries.forEach(function (x) {
          if (base[x.key]) return;
          base[x.key] = 1;
          if (docHidden()) tlHidden.push(x);
          else if (!shHidden) tlNotice(x);
        });
      }
    }
    tlRetitle();
  }
  function tlNotice(x) {
    if (!window.WsToast) return;
    var text = x.label + " · " + x.title + (x.author ? " · " + x.author : "");
    window.WsToast.show(text, { kind: "notice", onClick: function () { if (tkApi) tkApi.openNotice(x); } });
  }
  function tlShown() {
    if (docHidden()) return;
    var u = tlLastU, still = {};
    if (u && u.ready) u.entries.forEach(function (x) { still[x.store + "\n" + x.key] = 1; });
    var n = tlHidden.filter(function (x) { return still[x.store + "\n" + x.key]; }).length;
    tlHidden = [];
    if (!n || !window.WsToast) return;
    var store = u.store;
    window.WsToast.show(wsTn("js.toast.newnotes", n), { kind: "summary", onClick: function () { if (tkApi) tkApi.openNotify(store); } });
  }

  var sseOn = true;
  function connected(on) {
    sseOn = on;
    // A drop may be a daemon replaced underneath: the permission support
    // read before it no longer counts (dashboard-leads.js permStreamSync).
    if (typeof permStreamSync === "function") permStreamSync(on);
    q(".dwrap > .top #sse").forEach(function (el) {
      el.classList.toggle("off", !on);
      el.title = wsT(on ? "js.sse.title.on" : "js.sse.title.off");
    });
    // The Workpanel's live indicator (dashboard-workpanel.js) follows it.
    if (typeof wpStreamSync === "function") wpStreamSync(on);
  }
  // openStream opens a live stream (the daemon's sse.go and stream_ws.go)
  // over WebSocket: a browser counts WebSocket connections apart from the six
  // HTTP/1.1 connections it keeps per host, so open streams (one per tab, one
  // more for an open support chat) never hold up the page GETs, tracker
  // reads, and sends that share those six. Each frame is {event, data}, the
  // SSE stream's event name and data; a ping is answered with a pong, which
  // keeps the daemon from dropping the connection as dead. on.open runs when
  // the connection opens, on.events[name](data) per event, and on.drop once
  // when the connection fails or closes, never after the returned close().
  function openStream(path, on) {
    var visible = !pageHidden();
    var ws = new WebSocket((location.protocol === "https:" ? "wss://" : "ws://") + location.host + path + (path.indexOf("?") < 0 ? "?" : "&") + "display=" + (visible ? "1" : "0")), over = false;
    function visibility() {
      if (over || ws.readyState !== WebSocket.OPEN) return;
      try { ws.send(JSON.stringify({ event: "visibility", visible: !pageHidden() })); } catch (x) {}
    }
    function cleanup() {
      if (!doc.removeEventListener) return;
      doc.removeEventListener("visibilitychange", visibility);
      doc.removeEventListener("ws-page-visibility", visibility);
    }
    if (doc.removeEventListener) {
      doc.addEventListener("visibilitychange", visibility);
      doc.addEventListener("ws-page-visibility", visibility);
    }
    function drop() {
      if (over) return;
      over = true;
      cleanup();
      try { ws.close(); } catch (x) {}
      if (window.wsDashboardLoginProbe) window.wsDashboardLoginProbe();
      if (on.drop) on.drop();
    }
    ws.onopen = function () { if (!over) { visibility(); if (on.open) on.open(); } };
    ws.onerror = drop;
    ws.onclose = drop;
    ws.onmessage = function (e) {
      if (over) return;
      var jTok = typeof WsJank !== "undefined" && WsJank ? WsJank.begin("stream", "ws.onmessage") : null;
      var evName = "";
      var rawBytes = typeof e.data === "string" ? e.data.length : 0;
      var skipCrumb = false;
      try {
        var m = null;
        try { m = JSON.parse(e.data); } catch (x) { return; }
        if (!m || typeof m.event !== "string") return;
        evName = m.event;
        if (m.event === "ping") { skipCrumb = true; try { ws.send('{"event":"pong"}'); } catch (x) {} return; }
        var fn = on.events && Object.prototype.hasOwnProperty.call(on.events, m.event) && on.events[m.event];
        if (typeof fn === "function") fn(m.data);
      } finally {
        if (jTok && !skipCrumb) WsJank.end(jTok, { event: evName, bytes: rawBytes });
      }
    };
    function send(payload) {
      if (over || ws.readyState !== WebSocket.OPEN) return false;
      var raw = typeof payload === "string" ? payload : JSON.stringify(payload);
      var bytes = typeof TextEncoder === "function" ? new TextEncoder().encode(raw).length : raw.length;
      if (bytes > 1024) return false;
      try { ws.send(raw); return true; } catch (x) { return false; }
    }
    return { close: function () { over = true; cleanup(); try { ws.close(); } catch (x) {} }, send: send };
  }
  // Immutable tree inputs keep each nested row independent of its children.
  // Slots are private transport comments; materialization removes every slot
  // before inserting server markup. Live, formatted DOM is never a baseline.
  var TREE_SLOT = /<!--ws-tree-row-(\d+)-->/g;
  function treeRowKey(n) {
    if (n.matches("div.agent")) {
      var d = n.querySelector(":scope > details[data-key]");
      var k = d && d.getAttribute("data-key");
      return k && k.indexOf("agent-") === 0 && k.length > 6 ? k : null;
    }
    return n.matches("details.peer[data-peer]") && n.getAttribute("data-peer") ? "peer-" + n.getAttribute("data-peer") : null;
  }
  function treeSlot(i) { return "<!--ws-tree-row-" + i + "-->"; }
  function treeSlots(html) { return Array.from(html.matchAll(TREE_SLOT), function (m) { return +m[1]; }); }
  function treeInputFull(html) {
    if (typeof html !== "string" || html.indexOf("<!--ws-tree-row-") >= 0) return null;
    var box = doc.createElement("div"), rows = Object.create(null), order = [], parents = [];
    box.innerHTML = html;
    var nodes = q("div.agent, details.peer[data-peer]", box);
    if (nodes.some(function (n, i) {
      var key = treeRowKey(n), p = n.parentElement && n.parentElement.closest("div.agent, details.peer[data-peer]");
      if (!key || rows[key]) return true;
      order.push(key); parents.push(p ? nodes.indexOf(p) : -1); rows[key] = {};
      return false;
    })) return null;
    nodes.slice().reverse().forEach(function (n) {
      var i = nodes.indexOf(n);
      rows[order[i]] = { html: n.outerHTML };
      n.replaceWith(doc.createComment("ws-tree-row-" + i));
    });
    return { shell: box.innerHTML, order: order, parents: parents, rows: rows };
  }
  function treeRowNode(html, key) {
    if (typeof html !== "string") return null;
    var box = doc.createElement("div"); box.innerHTML = html;
    var n = box.firstElementChild;
    return box.childNodes.length === 1 && n && treeRowKey(n) === key && !n.querySelector("div.agent, details.peer[data-peer]") ? n : null;
  }
  function keyedSame(a, b) { return a.length === b.length && a.every(function (v, i) { return v === b[i]; }); }
  function keyedInputPatch(base, ev, rowNode, validate) {
    if (!base || !ev || typeof ev.hash !== "string" || !Array.isArray(ev.order) || !keyedSame(base.order, ev.order) || !Array.isArray(ev.rows)) return null;
    var rows = Object.create(null), changed = Object.create(null);
    Object.keys(base.rows).forEach(function (key) { rows[key] = base.rows[key]; });
    if (ev.rows.some(function (r) {
      if (!r || typeof r.key !== "string" || !rows[r.key] || changed[r.key] || typeof r.hash !== "string") return true;
      var n = rowNode(r.html, r.key);
      if (!n || (validate && !validate(n, r.key))) return true;
      changed[r.key] = true; rows[r.key] = { html: n.outerHTML };
      return false;
    })) return null;
    var next = { shell: base.shell, order: base.order, rows: rows };
    if (base.parents) next.parents = base.parents;
    return next;
  }
  function keyedInputHTML(input, slot, recursive) {
    if (typeof input === "string") return input;
    function fill(html) {
      return html.replace(slot, function (_, i) {
        var row = input.rows[input.order[+i]].html;
        return recursive ? fill(row) : row;
      });
    }
    return fill(input.shell);
  }
  function treeInputPatch(base, ev) {
    return keyedInputPatch(base, ev, treeRowNode, function (n, key) {
      return keyedSame(treeSlots(n.outerHTML), treeSlots(base.rows[key].html));
    });
  }
  function treeInputHTML(input) { return keyedInputHTML(input, TREE_SLOT, true); }
  // Validate every replacement before touching the live tree. Moving retained
  // children into a changed parent's slots preserves their node identities,
  // disclosure state, spinner and focus, even across simultaneous row changes.
  function mergeTreeRows(root, previous, next) {
    if (!previous || !next || previous.shell !== next.shell || !keyedSame(previous.order, next.order) || !keyedSame(previous.parents, next.parents)) return null;
    var nodes = q("div.agent, details.peer[data-peer]", root), live = Object.create(null), updates = Object.create(null), inserted = [];
    if (nodes.length !== previous.order.length || nodes.some(function (n, i) {
      var key = treeRowKey(n), p = n.parentElement && n.parentElement.closest("div.agent, details.peer[data-peer]");
      if (key !== previous.order[i] || (p ? nodes.indexOf(p) : -1) !== previous.parents[i]) return true;
      live[key] = n; return false;
    })) return null;
    if (next.order.some(function (key) {
      if (previous.rows[key].html === next.rows[key].html) return false;
      var n = treeRowNode(next.rows[key].html, key);
      if (!n || !keyedSame(treeSlots(n.outerHTML), treeSlots(previous.rows[key].html))) return true;
      updates[key] = n; return false;
    })) return null;
    next.order.slice().reverse().forEach(function (key) {
      var n = updates[key];
      if (!n) return;
      var walk = doc.createTreeWalker(n, 128), slots = [], slot;
      while ((slot = walk.nextNode())) if (/^ws-tree-row-\d+$/.test(slot.data)) slots.push(slot);
      slots.forEach(function (s) { s.replaceWith(live[next.order[+s.data.slice(12)]]); });
      live[key].replaceWith(n); live[key] = n; inserted.push(n);
    });
    return inserted;
  }

  // Panel transport rows are the existing top-level cards, not the nested
  // agenda disclosures. Canonical inputs never include reader folds, formatted
  // times, drawer styles or scroll offsets from the live DOM.
  var PANEL_SLOT = /<!--ws-panel-row-(\d+)-->/g;
  function panelRowKey(n) { return n.matches("details.panel") && /^p-.+/.test(n.id) ? n.id : null; }
  function panelInputFull(html) {
    if (typeof html !== "string" || html.indexOf("<!--ws-panel-row-") >= 0) return null;
    var box = doc.createElement("div"), rows = Object.create(null), order = [];
    box.innerHTML = html;
    if (Array.from(box.childNodes).some(function (n) { return n.nodeType !== 1 && n.nodeType !== 8 && (n.nodeType !== 3 || n.textContent.trim()); })) return null;
    if (Array.from(box.children).some(function (n) {
      var key = panelRowKey(n);
      if (!key || rows[key] || n.querySelector(".panel")) return true;
      rows[key] = { html: n.outerHTML }; order.push(key);
      n.replaceWith(doc.createComment("ws-panel-row-" + (order.length - 1)));
      return false;
    })) return null;
    return { shell: box.innerHTML, order: order, rows: rows };
  }
  function panelRowNode(html, key) {
    if (typeof html !== "string" || html.indexOf("<!--ws-panel-row-") >= 0) return null;
    var box = doc.createElement("div"); box.innerHTML = html;
    var n = box.firstElementChild;
    return box.childNodes.length === 1 && n && panelRowKey(n) === key && !n.querySelector(".panel") ? n : null;
  }
  function panelInputPatch(base, ev) { return keyedInputPatch(base, ev, panelRowNode); }
  function panelInputHTML(input) { return keyedInputHTML(input, PANEL_SLOT); }
  function mergePanelRows(root, previous, next) {
    if (!previous || !next || previous.shell !== next.shell || !keyedSame(previous.order, next.order)) return null;
    var live = Object.create(null), updates = [], nodes = Array.from(root.children);
    if (nodes.length !== previous.order.length || nodes.some(function (n, i) {
      var key = panelRowKey(n);
      if (key !== previous.order[i]) return true;
      live[key] = n; return false;
    })) return null;
    if (next.order.some(function (key) {
      if (previous.rows[key].html === next.rows[key].html) return false;
      var n = panelRowNode(next.rows[key].html, key);
      if (!n) return true;
      updates.push(n); return false;
    })) return null;
    updates.forEach(function (n) { live[n.id].replaceWith(n); });
    return updates;
  }
  // Cards are themselves scrollers on wide screens. Preserve their offsets
  // after a panels swap lays out replacements, and remap a changed card's focused
  // keyed disclosure without touching an unchanged card's content/frame nodes.
  function panelReaderState(root) {
    var active = doc.activeElement, focus = null;
    if (active && root.contains(active)) {
      var card = active.closest("details.panel"), anchor = active.closest("[data-key]") || card, path = [], n = active;
      if (card && anchor) {
        while (n !== anchor) { path.unshift(Array.prototype.indexOf.call(n.parentElement.children, n)); n = n.parentElement; }
        focus = { old: active, card: card.id, key: anchor.getAttribute("data-key"), path: path, tag: active.tagName, href: active.getAttribute("href") };
      }
    }
    return { cards: Array.from(root.children).map(function (n) { return { key: n.id, top: n.scrollTop, left: n.scrollLeft }; }), focus: focus };
  }
  function restorePanelReader(root, state) {
    var cards = Array.from(root.children);
    state.cards.forEach(function (s) { var n = cards.find(function (c) { return c.id === s.key; }); if (n) { n.scrollTop = s.top; n.scrollLeft = s.left; } });
    var f = state.focus;
    if (!f || f.old.isConnected) return;
    var card = cards.find(function (c) { return c.id === f.card; }), n = card;
    if (card && f.key) n = q("[data-key]", card).find(function (d) { return d.getAttribute("data-key") === f.key; });
    f.path.forEach(function (i) { n = n && n.children[i]; });
    if (n && n.tagName === f.tag && n.getAttribute("href") === f.href) n.focus({ preventScroll: true });
  }

  var ASSETS_TRIED = "ws.dashboard.assetsreload";
  var mainStream = null, mainRetry = null, mainProbe = null, mainEpoch = 0, mainPaused = false;
  // Cold loads and long disconnects (> 60s, e.g. sleep/wake) silently
  // re-baseline rail attention so stale arrivals never storm; short reconnects
  // (<= 60s, e.g. daemon swap or delta recovery) preserve seen asks so genuine
  // new questions arriving during the blip still flag and flash.
  var RAIL_RECONNECT_GRACE_MS = 60000, railBaselined = false, railDisconnectedAt = null;
  function railNoteDisconnect() {
    if (railBaselined && railDisconnectedAt === null) railDisconnectedAt = Date.now();
  }
  var companionRelay = null, companionStreamReady = !window.WebSocket;
  function companionStream(ready) {
    companionStreamReady = ready;
    if (companionRelay) companionRelay.reconcile(ready);
  }
  function stopMainStream() {
    mainEpoch++;
    if (mainRetry !== null) { clearTimeout(mainRetry); mainRetry = null; }
    if (mainProbe) { mainProbe.abort(); mainProbe = null; }
    if (mainStream) { railNoteDisconnect(); mainStream.close(); mainStream = null; }
  }
  function syncMainStream() {
    // Browser-hidden tabs retain their stream; only cached frames stop it.
    if (shHidden === mainPaused) return;
    mainPaused = shHidden;
    if (mainPaused) { railNoteDisconnect(); boardReturnEnd(); stopMainStream(); connected(false); trackerLost(); }
    else { boardReturnShown(); connect(); }
  }
  doc.addEventListener("ws-page-visibility", syncMainStream);
  // Local shell bridge heartbeat: opts this local top-level shell tab into the
  // daemon's multi-tab registry over the existing /rail/events WebSocket without
  // opening a second stream. Every frame stays well within wsMaxFrame (1 KiB).
  var daemonBridgeEnabled = !!(shell && root.hasAttribute("data-sysopen"));
  var daemonTabId = "";
  if (daemonBridgeEnabled) {
    daemonTabId = (function () {
      try {
        if (window.crypto && typeof window.crypto.getRandomValues === "function") {
          var b = new Uint8Array(8);
          window.crypto.getRandomValues(b);
          return "tab-" + Array.prototype.map.call(b, function (n) { return (n < 16 ? "0" : "") + n.toString(16); }).join("");
        }
      } catch (_) {}
      return "tab-" + Math.random().toString(36).slice(2, 10) + Date.now().toString(36);
    })();
    window.wsDaemonTabId = daemonTabId;
  }
  var daemonLastFocusedAt = 0;
  function sendDaemonHeartbeat(focusedOverride) {
    if (!daemonBridgeEnabled || !mainStream || typeof mainStream.send !== "function") return;
    var visible = !pageHidden();
    var focused = visible && (typeof focusedOverride === "boolean" ? focusedOverride : (typeof doc.hasFocus === "function" ? !!doc.hasFocus() : true));
    if (focused || (visible && !daemonLastFocusedAt)) daemonLastFocusedAt = Date.now();
    var activeRoute = String(location.pathname || "/").slice(0, 256);
    var activeReplyId = String(rid || "").slice(0, 64);
    var nativeCapture = !!(window.wsDaemonNativeCapture && typeof window.wsDaemonNativeCapture.capture === "function");
    mainStream.send({
      event: "tab-heartbeat",
      tab_id: daemonTabId,
      visible: visible,
      focused: focused,
      last_focused_at: daemonLastFocusedAt,
      active_route: activeRoute,
      active_reply_id: activeReplyId,
      capabilities: { confirm_banner: true, native_capture: nativeCapture }
    });
  }
  if (daemonBridgeEnabled) {
    window.addEventListener("focus", function () { sendDaemonHeartbeat(true); });
    window.addEventListener("blur", function () {
      // Moving focus into a same-origin session iframe blurs window while
      // document.hasFocus() stays true; defer one tick to distinguish real blur.
      setTimeout(function () {
        sendDaemonHeartbeat(typeof doc.hasFocus === "function" ? !!doc.hasFocus() : false);
      }, 0);
    });
    doc.addEventListener("visibilitychange", function () { sendDaemonHeartbeat(); });
    doc.addEventListener("ws-page-visibility", function () { sendDaemonHeartbeat(); });
    doc.addEventListener("ws-shell-route", function () { sendDaemonHeartbeat(); });
    doc.addEventListener("ws-shell-focus", function () { sendDaemonHeartbeat(true); });
    doc.addEventListener("ws-daemon-native-capture-ready", function () { sendDaemonHeartbeat(); });
  }
  function connect(withoutBoard) {
    if (!window.WebSocket || shHidden) return;
    if (framed && rid) companionStream(false);
    stopMainStream();
    var epoch = mainEpoch;
    function current() { return epoch === mainEpoch && !shHidden; }
    function retry(fn, delay) {
      if (!current()) return;
      if (mainRetry !== null) clearTimeout(mainRetry);
      mainRetry = setTimeout(function () { mainRetry = null; if (current()) fn(); }, delay);
    }
    // held() advertises a hidden update first, otherwise the applied region.
    // Use its immutable canonical input, never the mounted/staged/pinned DOM.
    var boardHeld = hiddenRegions.board || boardLatest;
    var boardInput = withoutBoard || !boardHeld ? null : (typeof boardHeld.html === "string" ? boardInputFull(boardHeld.html) : boardHeld.html);
    var railInput = null, treeInput = null, entryInput = null, panelInput = null, railFirst = true;
    // The shell streams the rail alone; a framed session page streams its
    // session without the rail.
    var path = shell ? "/rail/events" : mode === "index" || worktree ? "/events" : "/s/" + rid + "/events";
    var st = openStream(path + "?h=" + encodeURIComponent(held(withoutBoard)) + (framed && mode !== "index" ? "&frame=1" : "") + (!shell && mode !== "index" ? "&topics=1" : "") + (shell || (!framed && mode !== "index") ? "&railrows=1" : "") + (!shell && !worktree && mode !== "index" ? "&treerows=1&entryrows=1&panelrows=1" : "") + (daemonBridgeEnabled ? "&bridge=1&tab_id=" + encodeURIComponent(daemonTabId) : ""), {
      open: function () {
        connected(true);
        if (daemonBridgeEnabled) sendDaemonHeartbeat();
      },
      // Reopen with the hashes held now, so a reconnect sends only what
      // changed since.
      drop: function () {
        railNoteDisconnect();
        if (framed && rid) companionStream(false);
        if (daemonBridgeEnabled && typeof clearDaemonConfirm === "function") clearDaemonConfirm();
        // A dropped stream hides the guide stop chip until the next push.
        if (daemonBridgeEnabled) doc.dispatchEvent(new CustomEvent("ws-daemon-guide-monitor", { detail: { active: false, monitors: [] } }));
        connected(false);
        trackerLost();
        retry(connect, 3000);
      },
      events: { assets: onAssets, "openai-consent": onOpenAIConsent, "account-changed": onAccountChanged, "daemon-confirm-request": onDaemonConfirmRequest, "daemon-confirm-clear": onDaemonConfirmClear, "daemon-bridge-request": onDaemonBridgeRequest, "daemon-guide-monitor": onDaemonGuideMonitor, companion: onCompanion, "companion-focus": onCompanionFocus, "companion-command": onCompanionCommand, "companion-phone": onCompanionPhone, "conv-reveal": onConvReveal, region: onRegion, "board-topics": onBoardTopics, "rail-rows": onRailRows, "tree-rows": onTreeRows, "entry-rows": onEntryRows, "panel-rows": onPanelRows, tracker: onTracker, usercmd: onUsercmd, execjobs: onExecJobs, gone: onGone }
    });
    mainStream = st;
    function onOpenAIConsent(event) {
      if (!framed && current()) showOpenAIConsent(event);
    }
    function onDaemonConfirmRequest(event) {
      if (!daemonBridgeEnabled || !current() || !event) return;
      if (typeof showDaemonConfirm === "function") showDaemonConfirm(event);
      doc.dispatchEvent(new CustomEvent("ws-daemon-confirm-request", { detail: event }));
    }
    function onDaemonConfirmClear(event) {
      if (!daemonBridgeEnabled || !current()) return;
      if (typeof clearDaemonConfirm === "function") clearDaemonConfirm(event && event.request_id);
      doc.dispatchEvent(new CustomEvent("ws-daemon-confirm-clear", { detail: event || {} }));
    }
    function onDaemonBridgeRequest(event) {
      if (!daemonBridgeEnabled || !current() || !event) return;
      doc.dispatchEvent(new CustomEvent("ws-daemon-bridge-request", { detail: event }));
    }
    function onDaemonGuideMonitor(event) {
      if (!daemonBridgeEnabled || !current() || !event) return;
      doc.dispatchEvent(new CustomEvent("ws-daemon-guide-monitor", { detail: event }));
    }
    function onAccountChanged(event) {
      if (!framed && current()) doc.dispatchEvent(new CustomEvent("ws-account-changed", { detail: event }));
    }
    function onCompanion(config) {
      if (!shell || !current() || !config) return;
      window.wsCompanionConfig = config;
      doc.dispatchEvent(new CustomEvent("ws-companion-config", { detail: config }));
    }
    // The daemon hands each shell stream a token its focus report echoes; a
    // reconnect gets a new one.
    function onCompanionFocus(focus) {
      if (!shell || !current() || !focus || typeof focus.token !== "string") return;
      window.wsCompanionFocusToken = focus.token;
      doc.dispatchEvent(new CustomEvent("ws-companion-focus", { detail: focus.token }));
    }
    // An agent-director command is broadcast to every shell; the companion
    // keeps only those for the session it shows.
    function onCompanionCommand(command) {
      if (!shell || !current() || !command) return;
      doc.dispatchEvent(new CustomEvent("ws-companion-command", { detail: command }));
    }
    function onCompanionPhone(snapshot) {
      if (!shell || !current() || !snapshot) return;
      window.wsCompanionPhoneState = snapshot;
      doc.dispatchEvent(new CustomEvent("ws-companion-phone", { detail: snapshot }));
    }
    function onConvReveal(ev) {
      if (!current() || !ev || typeof ev.replyId !== "string" || !ev.replyId) return;
      if (typeof railReveal === "function") railReveal(ev);
    }
    function onRegion(ev) {
      if (!current() || !ev) return;
      if (worktree && ev.name !== "rail") return;
      if (ev.name === "mode") {
        if (ev.html !== mode) modeChanged(ev.html);
        modeHash = ev.hash;
        liveMode = ev.html === "live";
        syncBadge();
        return;
      }
      if (ev.name === "board") {
        boardInput = boardInputFull(ev.html);
        if (boardInput) ev = { name: "board", hash: ev.hash, html: boardInput };
      }
      if (ev.name === "rail" && WsRail) {
        railInput = WsRail.rowInputFull(ev.html, doc);
        if (railInput) ev = { name: "rail", hash: ev.hash, html: railInput };
      }
      if (ev.name === "tree") {
        treeInput = treeInputFull(ev.html);
        if (treeInput) ev = { name: "tree", hash: ev.hash, html: treeInput };
      }
      if (ev.name === "entries") {
        entryInput = entryInputFull(ev.html);
        if (entryInput) ev = { name: "entries", hash: ev.hash, html: entryInput };
      }
      if (ev.name === "panels") {
        panelInput = panelInputFull(ev.html);
        if (panelInput) ev = { name: "panels", hash: ev.hash, html: panelInput };
      }
      receiveRegion(ev);
    }
    function onPanelRows(ev) {
      var next = panelInputPatch(panelInput, ev);
      if (!next) {
        railNoteDisconnect();
        st.close(); connected(false);
        retry(connect, 0);
        return;
      }
      panelInput = next;
      receiveRegion({ name: "panels", hash: ev.hash, html: next });
    }
    function onEntryRows(ev) {
      var next = entryInputPatch(entryInput, ev);
      if (!next) {
        railNoteDisconnect();
        st.close(); connected(false);
        retry(connect, 0);
        return;
      }
      entryInput = next;
      receiveRegion({ name: "entries", hash: ev.hash, html: next });
    }
    function onTreeRows(ev) {
      var next = treeInputPatch(treeInput, ev);
      if (!next) {
        railNoteDisconnect();
        st.close(); connected(false);
        retry(connect, 0);
        return;
      }
      treeInput = next;
      receiveRegion({ name: "tree", hash: ev.hash, html: next });
    }
    function onRailRows(ev) {
      var next = WsRail && WsRail.rowInputPatch(railInput, ev, doc);
      if (!next) {
        // Close before reopening: the new connection always sends a full rail.
        railNoteDisconnect();
        st.close(); connected(false);
        retry(connect, 0);
        return;
      }
      railInput = next;
      receiveRegion({ name: "rail", hash: ev.hash, html: next });
    }
    function onBoardTopics(ev) {
      var next = boardInputPatch(boardInput, ev);
      if (!next) {
        // This connection's baseline cannot be trusted. Close it before
        // reopening without the board hash; other regions keep their hashes.
        railNoteDisconnect();
        st.close();
        connected(false);
        retry(function () { connect(true); }, 0);
        return;
      }
      boardInput = next;
      receiveRegion({ name: "board", hash: ev.hash, html: next });
    }
    function receiveRegion(ev) {
      var jTok = typeof WsJank !== "undefined" && WsJank ? WsJank.begin("stream", "receiveRegion") : null;
      try {
      if (ev.name === "board" && companionRelay) companionRelay.board(ev.html);
      // Observe even held updates: an ask may open and close while hidden
      // or during a drag. Cold loads and long disconnects (> 60s) baseline
      // silently; short reconnects compare against pre-disconnect seen asks.
      var railBaseline = ev.name === "rail" && railFirst && (!railBaselined || (railDisconnectedAt !== null && Date.now() - railDisconnectedAt > RAIL_RECONNECT_GRACE_MS));
      var needCompanionRail = !!(shell && window.WsCompanion && window.WsCompanion.observeRail && (!window.WsCompanion.hasRailObserver || window.WsCompanion.hasRailObserver()));
      var fastRailRows = ev.name === "rail" && railAttention && !railBaseline && !needCompanionRail && !pageHidden() && WsRail && WsRail.rowInputAttentionRows
        ? WsRail.rowInputAttentionRows(ev.html)
        : null;
      var railFrag = ev.name === "rail" && railAttention && !fastRailRows ? frag(WsRail.rowInputHTML(ev.html)) : null;
      if (fastRailRows) railAttention.observe(fastRailRows, rid, railBaseline);
      else if (railFrag) railAttention.observe(q(".rs", railFrag), rid, railBaseline);
      if (railFrag && shell) window.WsCompanion?.observeRail?.(railFrag, rid, railBaseline);
      if (railFrag && railBaseline) apScan(railFrag, "rail", true);
      if (ev.name === "rail") { railFirst = false; railBaselined = true; railDisconnectedAt = null; }
      // A hidden tab keeps only each region's newest update and applies none;
      // showing the tab applies them (showRegions). The stream stays open, so
      // the light events (tracker) still arrive and act.
      if (pageHidden()) {
        delete hiddenRegions[ev.name];
        hiddenRegions[ev.name] = { hash: ev.hash, html: ev.html };
        tlHold(ev.name, ev.hash, ev.html);
        if (ev.name === "rail" || ev.name === "sessions") apScan(railFrag || frag(ev.html), ev.name);
        companionSettled(ev);
        return;
      }
      if (ev.name === "board" && boardReturnTimer) boardReturnFresh = true;
      swap(ev.name, ev.hash, ev.html);
      companionSettled(ev);
      } finally {
        if (jTok) WsJank.end(jTok, { region: ev && typeof ev.name === "string" ? ev.name : "", bytes: ev && typeof ev.html === "string" ? ev.html.length : 0 });
      }
    }
    function companionSettled(ev) {
      // Keyed panels arrive even at an unchanged hash and follow board,
      // tree and entries in the stream's initial region order.
      if (framed && rid && !companionStreamReady && ev.name === "panels") companionStream(true);
    }
    // The lead's user commands changed (an opaque revision): the composer's
    // command strip refetches its list when the revision is not the one it holds.
    function onUsercmd(ev) {
      if (!ev || typeof ev.rev !== "string" || !rid) return;
      if (typeof ucmdWake === "function") ucmdWake(ev.rev);
    }
    // The exec job HUD's opaque revision: the job set changed (or the stream
    // just connected), so the page reads its own job list. No content rides it.
    function onExecJobs(ev) {
      if (!ev || typeof ev.rev !== "number" || !rid) return;
      if (typeof execWake === "function") execWake(ev.rev);
      if (typeof bgExecWake === "function") bgExecWake(ev.rev);
    }
    // A frame the shell hides reads the tracker once it shows (shShown).
    function onTracker(ev) {
      if (!ev || typeof ev.rev !== "number") return;
      if (shHidden) shTrackerRev = ev.rev;
      else trackerNudge(ev.rev);
    }
    // Every (re)connect first names the page assets the daemon serves now:
    // other ones than this page runs (a replaced daemon) reload it, as
    // modeChanged does, or banner while a draft is held. A daemon that names
    // none, or a page rendered without one, never acts. A reload already
    // tried for the same assets (per tab) banners instead, so a page can
    // never reload in a loop.
    function onAssets(ev) {
      var mine = root.getAttribute("data-assets"), tried = null;
      if (!ev || !ev.id || !mine || ev.id === mine) return;
      try { tried = window.sessionStorage.getItem(ASSETS_TRIED); } catch (x) {}
      if (holdsDraft() || shDrafts() || tried === ev.id) { banner(wsT("js.banner.updated")); return; }
      try { window.sessionStorage.setItem(ASSETS_TRIED, ev.id); } catch (x) {}
      boardSaveReload();
      // Only the active frame returns with the shell reload; inactive
      // frames would leave unreachable entries in the tab's storage.
      if (shell && shOn) {
        try { if (shOn.el.contentWindow.wsSaveBoardReload) shOn.el.contentWindow.wsSaveBoardReload(); } catch (e) {}
      }
      location.reload();
    }
    // A WebSocket handshake hides HTTP status. Recheck the stream once after
    // the usual retry delay before calling the session gone; never replace
    // the held DOM (or its drafts) with the probe's response. Transient probe
    // failures keep retrying at that cadence until absence is confirmed or
    // the stream can reopen with the hashes held then.
    function onGone() {
      railNoteDisconnect();
      st.close();
      if (framed && rid) companionStream(false);
      connected(false);
      trackerLost();
      retry(recheckGone, 3000);
    }
    function recheckGone() {
      if (!current()) return;
      var ctl = typeof AbortController === "function" ? new AbortController() : null;
      mainProbe = ctl;
      var options = { cache: "no-store" };
      if (ctl) options.signal = ctl.signal;
      fetch(path + (framed && mode !== "index" ? "?frame=1" : ""), options).then(function (res) {
        // Cancel the response even if it arrived after the frame was hidden.
        if (res.body) res.body.cancel().catch(function () {});
        if (!current()) return;
        mainProbe = null;
        if (res.status === 404) { banner(wsT("js.banner.gone")); return; }
        if (res.ok) { connect(); return; }
        retry(recheckGone, 3000);
      }).catch(function () { if (current()) { mainProbe = null; retry(recheckGone, 3000); } });
    }
  }
  // showRegions applies, once each and in arrival order, the region updates
  // a hidden tab held back.
  function showRegions() {
    if (pageHidden()) return;
    var p = hiddenRegions;
    hiddenRegions = {};
    var keys = Object.keys(p);
    var jTok = typeof WsJank !== "undefined" && WsJank ? WsJank.begin("stream", "showRegions") : null;
    try {
      keys.forEach(function (name) { swap(name, p[name].hash, p[name].html); });
      tlHeld = {};
      tlCount();
      tlShown();
    } finally {
      if (jTok) WsJank.end(jTok, { count: keys.length });
    }
  }
  doc.addEventListener("visibilitychange", showRegions);
  function banner(text) {
    var b = $("dash-banner");
    if (!b) return;
    b.textContent = text;
    b.hidden = false;
  }
  // holdsDraft reports unsent work a reload would drop: composer text,
  // answers picked on the board, an attached image, or a topic close request.
  function holdsDraft() { return !!((ctext && ctext.value) || cmdSel || boardAnswers() || imgs.length || topicNotes().length || closeRequests.length || outboxUnsent()); }
  // The shell asks its frames before an assets reload, which reloads them
  // too (shDrafts).
  if (framed) window.wsHoldsDraft = holdsDraft;
  function modeChanged(next) {
    // The other mode has other regions, and a reload renders them; text typed
    // in the composer or answers picked on the board are never dropped for it.
    if (holdsDraft()) {
      banner(wsT(next === "live" ? "js.banner.appeared" : "js.banner.wentaway"));
      return;
    }
    location.reload();
  }

