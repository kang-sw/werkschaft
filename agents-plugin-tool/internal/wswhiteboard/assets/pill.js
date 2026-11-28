;(function () {
  // Open-question pill and topic-header ask breakdown. Items awaiting the user
  // (open policy, ack, check) scrolled out of the board's scroll area show a
  // floating pill at that edge, split into still-open items, daemon-held items,
  // and items already answered (the form holds a reply, or the dashboard marked
  // one sent or held). Each topic's .tophead .tbar mirrors the policy+check
  // open/answered/held split, and the tab title carries the still-open total.
  // The dashboard re-runs this after every board swap, so it tears down what
  // the previous run attached.
  var d = document;
  if (!window.IntersectionObserver) return;
  var host = d.getElementById("bcol") || d.querySelector(".wrap");
  if (!host) return;
  var st = window.wbPill || (window.wbPill = {});
  if (st.off) st.off();

  var items = Array.prototype.slice.call(d.querySelectorAll('.card.policy[id^="item-"], .arow[id^="item-"], .ckrow[data-id]:not([data-assumed])'));

  function itemKey(el) {
    if (!el || !el.getAttribute) return "";
    return el.getAttribute("data-id") || el.getAttribute("id") || "";
  }

  // An item's draft state, read from the same form controls form.js builds
  // the reply from: a pick, a ticked ack, or a non-empty note is a reply
  // segment ("responded"); a note alone also counts as "noted". Item fields
  // keep their class attribute, so each control is selected by what a field
  // cannot carry (a data-* attribute, a textarea or label element), as
  // form.js does.
  function noted(el) {
    var n = el.querySelector("textarea.note");
    return !!(n && n.value.trim());
  }
  function responded(el) {
    var box = el.querySelector("label.ack > input, label.ck > input");
    return !!(el.querySelector('.opt[data-opt][aria-pressed="true"]') || (box && box.checked) || el.hasAttribute("data-no") || noted(el));
  }
  // The dashboard marks an item it already sent, or holds, a reply for with
  // sent-sent / sent-held; that item is no longer open either.
  function answered(el) {
    return responded(el) || el.classList.contains("sent-sent") || el.classList.contains("sent-held");
  }

  function sessionReplyId() {
    var html = d.documentElement;
    if (!html || !html.getAttribute) return "";
    return html.getAttribute("data-rid") || html.getAttribute("data-wb-reply") || "";
  }
  function holdRouteURL() {
    var rid = sessionReplyId();
    return rid ? "/s/" + encodeURIComponent(rid) + "/board/hold" : "";
  }
  function normItemId(k) {
    var s = String(k || "").trim();
    if (/^[0-9]+$/.test(s)) return String(parseInt(s, 10));
    return s;
  }
  var currentRid = sessionReplyId();
  if (st._holdRid !== currentRid) {
    st._holdRid = currentRid;
    st._holdPending = {};
    st._holdDraftCleared = {};
    st._holdSeq = 0;
  }
  var holdPending = st._holdPending || (st._holdPending = {});
  var holdDraftCleared = st._holdDraftCleared || (st._holdDraftCleared = {});
  function holdButton(el) {
    var b = el && el.querySelector ? el.querySelector(".wb-hold-toggle") : null;
    return b && b.hasAttribute && b.hasAttribute("data-wb-hold") ? b : null;
  }
  function domHeld(el) {
    if (!el || !el.classList) return false;
    if (el.classList.contains("wb-held")) return true;
    var b = holdButton(el);
    return !!(b && b.getAttribute("aria-pressed") === "true");
  }
  function syncHoldDOM(el, on) {
    if (el.classList && el.classList.contains("wb-held") !== on) el.classList.toggle("wb-held", on);
    var b = holdButton(el);
    if (!b) return;
    var pressed = on ? "true" : "false";
    if (b.getAttribute("aria-pressed") !== pressed) b.setAttribute("aria-pressed", pressed);
    var tip = on ? wsT("js.pill.unhold") : wsT("js.pill.hold");
    if (b.title !== tip) b.title = tip;
    if (b.getAttribute("aria-label") !== tip) b.setAttribute("aria-label", tip);
  }
  function applyServerHeld(list) {
    if (!Array.isArray(list)) return;
    var set = {};
    list.forEach(function (id) {
      var nid = normItemId(id);
      if (nid) set[nid] = true;
    });
    items.forEach(function (el) {
      var nid = normItemId(itemKey(el));
      if (!nid || holdPending[nid]) return;
      syncHoldDOM(el, !!set[nid] && !answered(el));
    });
    syncTopicBars();
    retitle();
    redrawSoon();
  }
  function revertHold(nid, prevHeld) {
    items.forEach(function (el) {
      if (normItemId(itemKey(el)) !== nid) return;
      if (holdPending[nid]) return;
      syncHoldDOM(el, !!prevHeld && !answered(el));
    });
    syncTopicBars();
    retitle();
    redrawSoon();
  }
  st._applyServerHeld = applyServerHeld;
  st._revertHold = revertHold;
  // A hold flip re-renders its topic, and page.tmpl changes only the hold
  // markers there: the item's wb-held class, its toggle's pressed state and
  // labels, the topic bar's ask chips (hidden, count) and the section's
  // data-th. holdOnly(prev, next) says two rendered topics differ in nothing
  // else, so the board swap keeps the live section (its frames stay mounted
  // and the free canvas keeps its layout) and adoptHolds takes the item
  // markers; the re-run of this script then redraws the chips.
  function holdAttrSkip(el, name) {
    if (name === "data-th") return el.matches("section.topic");
    if (name === "hidden") return el.hasAttribute("data-tbar-ask");
    return el.hasAttribute("data-wb-hold") && (name === "aria-pressed" || name === "title" || name === "aria-label");
  }
  function holdClass(el) {
    return (el.getAttribute("class") || "").split(/\s+/).filter(function (c) { return c && c !== "wb-held"; }).join(" ");
  }
  function holdSame(a, b) {
    if (a.nodeType !== b.nodeType) return false;
    if (a.nodeType !== 1) {
      if (a.nodeValue === b.nodeValue) return true;
      var p = a.parentNode;
      return a.nodeType === 3 && !!p && p.classList.contains("n") && !!p.parentNode && p.parentNode.hasAttribute("data-tbar-ask");
    }
    if (a.tagName !== b.tagName) return false;
    var names = {}, i;
    for (i = 0; i < a.attributes.length; i++) names[a.attributes[i].name] = true;
    for (i = 0; i < b.attributes.length; i++) names[b.attributes[i].name] = true;
    for (var name in names) {
      if (holdAttrSkip(a, name)) continue;
      if (name === "class" ? holdClass(a) !== holdClass(b) : a.getAttribute(name) !== b.getAttribute(name)) return false;
    }
    var ac = a.childNodes, bc = b.childNodes;
    if (ac.length !== bc.length) return false;
    for (i = 0; i < ac.length; i++) if (!holdSame(ac[i], bc[i])) return false;
    return true;
  }
  st.holdOnly = function (prev, next) { return !!(prev && next && prev.nodeType === 1 && holdSame(prev, next)); };
  st.adoptHolds = function (live, next) {
    var at = {};
    Array.prototype.forEach.call(live.querySelectorAll('[id^="item-"]'), function (el) { at[el.id] = el; });
    Array.prototype.forEach.call(next.querySelectorAll(".wb-hold-toggle[data-wb-hold]"), function (b) {
      var src = b.closest('[id^="item-"]'), el = src && at[src.id];
      if (el) syncHoldDOM(el, domHeld(src));
    });
  };
  function postHold(rawId, next, prevHeld, acceptGone) {
    var nid = normItemId(rawId);
    if (!nid) return;
    var seq = ++st._holdSeq;
    var reqRid = currentRid;
    holdPending[nid] = { seq: seq, held: !!next };
    var url = holdRouteURL();
    if (!url || typeof window.fetch !== "function") {
      delete holdPending[nid];
      return;
    }
    window.fetch(url, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id: String(rawId), held: !!next })
    }).then(function (resp) {
      if (st._holdRid !== reqRid) return;
      var stillCurrent = holdPending[nid] && holdPending[nid].seq === seq;
      if (stillCurrent) delete holdPending[nid];
      if (resp && resp.ok) {
        return resp.json().then(function (data) {
          if (st._holdRid !== reqRid || st._holdSeq !== seq) return;
          if (data && Array.isArray(data.held) && st._applyServerHeld) st._applyServerHeld(data.held);
        }, function () {});
      }
      if (acceptGone && resp && (resp.status === 404 || resp.status === 409)) return;
      if (stillCurrent && !acceptGone && st._revertHold) st._revertHold(nid, prevHeld);
    }, function () {
      if (st._holdRid !== reqRid) return;
      var stillCurrent = holdPending[nid] && holdPending[nid].seq === seq;
      if (stillCurrent) {
        delete holdPending[nid];
        if (!acceptGone && st._revertHold) st._revertHold(nid, prevHeld);
      }
    });
  }
  // Sync DOM to server-rendered held state (plus any in-flight optimistic
  // toggle); when a draft or sent mark lands on a held item, clear the hold
  // once through the daemon route (404/409 treated as already cleared).
  function applyHoldDOM() {
    items.forEach(function (el) {
      var rawId = itemKey(el);
      var nid = normItemId(rawId);
      if (!nid) return;
      var pend = holdPending[nid];
      var currentlyHeld = pend ? !!pend.held : domHeld(el);
      if (answered(el)) {
        if (currentlyHeld || domHeld(el)) {
          syncHoldDOM(el, false);
          if (!holdDraftCleared[nid]) {
            holdDraftCleared[nid] = true;
            postHold(rawId, false, false, true);
          }
        }
        return;
      }
      if (pend) {
        syncHoldDOM(el, !!pend.held);
      } else {
        var on = domHeld(el);
        if (on) delete holdDraftCleared[nid];
        syncHoldDOM(el, on);
      }
    });
  }
  function held(el) {
    if (!el || answered(el)) return false;
    var nid = normItemId(itemKey(el));
    if (nid && holdPending[nid]) return !!holdPending[nid].held;
    return domHeld(el);
  }
  function isHeldExternal(el) {
    if (!el) return false;
    var nid = normItemId(itemKey(el));
    if (nid && holdPending[nid]) return !!holdPending[nid].held;
    return domHeld(el);
  }
  function isTopicAsk(el) {
    return !!(el && el.classList && (el.classList.contains("policy") || el.classList.contains("ckrow")));
  }
  function topicBarOf(topic) {
    if (!topic || !topic.querySelector) return null;
    try {
      var direct = topic.querySelector(":scope > .tophead > .tbar");
      if (direct) return direct;
    } catch (e) {}
    return topic.querySelector(".tbar");
  }
  function patchAskChip(bar, sel, count) {
    var chip = bar.querySelector(sel);
    if (!chip) return false;
    var changed = false;
    var hide = count <= 0;
    if (chip.hidden !== hide) { chip.hidden = hide; changed = true; }
    var num = chip.querySelector ? chip.querySelector(".n") : null;
    var text = String(count);
    if (num) {
      if (num.textContent !== text) { num.textContent = text; changed = true; }
    } else if (chip.textContent !== text) {
      chip.textContent = text;
      changed = true;
    }
    return changed;
  }
  // Sync each topic's .tophead .tbar policy+check open/answered/held chips
  // (excluding acks, which keep .chip.ok) and mirror updates to any active
  // Overview placeholder. Performs zero layout reads.
  function syncTopicBars() {
    var byTopic = new Map();
    items.forEach(function (el) {
      if (!isTopicAsk(el)) return;
      var topic = el.closest ? el.closest("section.topic") : null;
      if (!topic) return;
      var c = byTopic.get(topic);
      if (!c) { c = { open: 0, ans: 0, held: 0 }; byTopic.set(topic, c); }
      if (answered(el)) c.ans++;
      else if (held(el)) c.held++;
      else c.open++;
    });
    var openChanged = false;
    byTopic.forEach(function (c, topic) {
      var bar = topicBarOf(topic);
      if (!bar) return;
      var polChanged = patchAskChip(bar, ".chip.policy", c.open);
      var ansChanged = patchAskChip(bar, ".chip.wb-chip-ans", c.ans);
      var heldChanged = patchAskChip(bar, ".chip.wb-chip-held", c.held);
      if (polChanged) openChanged = true;
      if ((polChanged || ansChanged || heldChanged) && window.WsOverview && window.WsOverview.syncPlaceholder) {
        window.WsOverview.syncPlaceholder(topic);
      }
    });
    if (openChanged && window.wbRail && window.wbRail.refresh) window.wbRail.refresh();
  }
  function toggleHold(el) {
    var rawId = itemKey(el);
    var nid = normItemId(rawId);
    if (!nid) return false;
    var wasHeld = holdPending[nid] ? !!holdPending[nid].held : domHeld(el);
    if (!wasHeld && answered(el)) return false;
    var next = !wasHeld;
    if (next) delete holdDraftCleared[nid];
    syncHoldDOM(el, next);
    postHold(rawId, next, wasHeld, !next);
    syncTopicBars();
    retitle();
    redrawSoon();
    return next;
  }

  applyHoldDOM();
  syncTopicBars();

  // The dashboard owns the document title; report the still-open total to
  // its hook when one is installed. Held items leave the open total.
  function retitle() {
    var n = items.filter(function (el) { return !answered(el) && !held(el); }).length;
    if (window.wbTitleCount) window.wbTitleCount(n);
  }

  // Two zero-height sticky wrappers, first and last in the host, carry the
  // pills so showing one never moves the board. A pill is a card-coloured
  // capsule holding the edge's arrow and one button per state, in the order
  // open, held, answered, noted; show() fills their counts and hides an empty
  // one. The buttons are built once, so a redraw never drops a focused one.
  var KINDS = ["open", "held", "ans", "nt"];
  function segLabel(k) {
    return k === "open" ? wsT("js.pill.open")
      : k === "held" ? wsT("js.pill.held")
      : k === "ans" ? wsT("js.pill.answered")
      : wsT("js.pill.noted");
  }
  function segTitle(edge, k) {
    if (edge === "top") {
      return k === "open" ? wsT("js.pill.up")
        : k === "held" ? wsT("js.pill.upheld")
        : k === "ans" ? wsT("js.pill.upanswered")
        : wsT("js.pill.upnoted");
    }
    return k === "open" ? wsT("js.pill.down")
      : k === "held" ? wsT("js.pill.downheld")
      : k === "ans" ? wsT("js.pill.downanswered")
      : wsT("js.pill.downnoted");
  }
  function wrapper(edge) {
    // Edge modifiers must not share the page header's bare .top class.
    var modifier = edge === "top" ? "wb-oq-top" : "wb-oq-bot";
    var w = host.querySelector(".oq-wrap." + modifier);
    if (!w) {
      w = d.createElement("div");
      w.className = "oq-wrap " + modifier;
      var p = d.createElement("div");
      p.className = "oq-pill";
      p.hidden = true;
      var arr = d.createElement(edge === "top" ? "button" : "span");
      arr.className = "oq-arr";
      if (edge === "top") {
        arr.type = "button";
        arr.title = wsT("js.board.hold.pill.title");
        arr.setAttribute("aria-label", arr.title);
      } else arr.setAttribute("aria-hidden", "true");
      arr.textContent = edge === "top" ? "↑" : "↓";
      if (edge === "top") arr.innerHTML = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 19V5M5 12l7-7 7 7"/></svg>';
      p.appendChild(arr);
      if (edge === "top") {
        var updates = d.createElement("button");
        updates.type = "button";
        updates.className = "oq-updates";
        updates.hidden = true;
        updates.title = arr.title;
        p.appendChild(updates);
        var pull = d.createElement("button");
        pull.type = "button";
        pull.className = "oq-pull";
        pull.hidden = true;
        pull.title = wsT("js.board.hold.pull");
        pull.setAttribute("aria-label", pull.title);
        pull.innerHTML = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 4v11M7 11l5 5 5-5M5 20h14"/></svg>';
        p.appendChild(pull);
      }
      KINDS.forEach(function (k) {
        var b = d.createElement("button");
        b.type = "button";
        b.className = "oq-seg " + (k === "held" ? "wb-oq-held" : k);
        b.setAttribute("data-k", k);
        b.hidden = true;
        var label = d.createElement("span");
        label.textContent = segLabel(k);
        var n = d.createElement("span");
        n.className = "n";
        b.appendChild(label);
        b.appendChild(n);
        b.title = segTitle(edge, k);
        p.appendChild(b);
      });
      w.appendChild(p);
      if (edge === "top") host.insertBefore(w, host.firstChild); else host.appendChild(w);
    }
    return w;
  }
  var wraps = { top: wrapper("top"), bot: wrapper("bot") };
  var pills = { top: wraps.top.firstChild, bot: wraps.bot.firstChild };
  var target = { top: {}, bot: {} };

  // The host is the scroll root when it scrolls on its own (the dashboard's
  // board column); otherwise the window scrolls.
  var root = /auto|scroll/.test(getComputedStyle(host).overflowY) ? host : null;
  // Observers only invalidate the display; they are not the pending-item
  // inventory. Their reports can describe layout before a frame height (or
  // a scroll compensation) landed. Mixing that snapshot with current topic
  // rects made static counts depend on observer delivery order.
  var positions = new Set();
  items.forEach(function (el) { positions.add(el.closest("section.topic") || el); });

  // list is ordered top to bottom; the nearest item at the top edge is the
  // last one, at the bottom edge the first. Each segment jumps to the nearest
  // item of its own state: still open, locally held, answered, or answered
  // with a note (noted items are a subset of the answered ones).
  function atTop() { return (root ? root.scrollTop : window.scrollY) <= 4; }
  function show(edge, list, width, home, goal) {
    var p = pills[edge], pending = edge === "top" && st.pending > 0;
    // The top capsule has nothing to offer at the very top with no board
    // update pending (no item above either); a latched change keeps it.
    var hide = edge === "top" ? !pending && !list.length && home : !list.length;
    var label = pending ? wsT("js.board.hold.pill") + " " + st.pending : "";
    var groups = { open: [], held: [], ans: [], nt: [] };
    list.forEach(function (el) {
      if (answered(el)) {
        groups.ans.push(el);
        if (noted(el)) groups.nt.push(el);
        return;
      }
      if (held(el)) { groups.held.push(el); return; }
      groups.open.push(el);
    });
    target[edge] = {};
    KINDS.forEach(function (k) { var g = groups[k]; if (g.length) target[edge][k] = edge === "top" ? g[g.length - 1] : g[0]; });
    var shape = [hide, label, groups.open.length, groups.held.length, groups.ans.length, groups.nt.length].join("|");
    if (shape === p.wbShape) {
      // The invisible copy supplies the new intrinsic width while the real
      // capsule still presents its old width. Both were read before writes.
      if (p.wbMeasure) {
        p.wbMeasure.parentNode.removeChild(p.wbMeasure);
        p.wbMeasure = null;
        p.style.width = "";
        var from = p.wbFrom;
        if (from && goal && from !== goal) {
          p.wbGrow = p.animate([{ width: from + "px" }, { width: goal + "px" }], { duration: 420, easing: "cubic-bezier(.34,1.56,.64,1)" });
          p.wbGrow.onfinish = function () { p.style.overflow = ""; };
        } else p.style.overflow = "";
      }
      return;
    }
    if (p.hidden !== hide) p.hidden = hide;
    p.classList.toggle("board-updated", !!pending);
    if (edge === "top") {
      var updates = p.querySelector(".oq-updates");
      if (updates.hidden !== !pending) updates.hidden = !pending;
      if (updates.textContent !== label) updates.textContent = label;
      var pull = p.querySelector(".oq-pull");
      if (pull.hidden !== !pending) pull.hidden = !pending;
    }
    Array.prototype.forEach.call(p.querySelectorAll(".oq-seg"), function (b) {
      var k = b.getAttribute("data-k"), g = groups[k] || [];
      if (b.hidden !== !g.length) b.hidden = !g.length;
      var count = b.querySelector(".n"), text = String(g.length), label = b.title + " (" + g.length + ")";
      if (count.textContent !== text) count.textContent = text;
      if (b.getAttribute("aria-label") !== label) b.setAttribute("aria-label", label);
    });
    // Measure the destination next frame, without flashing it before the
    // grow starts. Only the real capsule holds a width; the hidden copy is
    // intrinsic and cannot receive focus or duplicate accessible controls.
    if (p.wbGrow) p.wbGrow.cancel();
    if (p.wbMeasure) p.wbMeasure.parentNode.removeChild(p.wbMeasure);
    p.wbMeasure = null;
    p.style.width = "";
    if (p.style.overflow) p.style.overflow = "";
    p.wbShape = shape;
    if (p.animate && width && !hide && !(window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches)) {
      var copy = p.cloneNode(true);
      copy.style.visibility = "hidden";
      copy.style.animation = "none";
      copy.style.pointerEvents = "none";
      copy.setAttribute("aria-hidden", "true");
      copy.inert = true;
      wraps[edge].appendChild(copy);
      p.wbMeasure = copy; p.wbFrom = width;
      p.style.width = width + "px";
      p.style.overflow = "clip";
      redrawSoon();
    }
  }
  // Avoid reading every far item's rect: an item in a topic wholly above
  // or below the view takes its side from the topic's rect, read once per
  // update, unless a closed fold hides it. Only an item in a topic crossing
  // the view reads its own rect.
  function folded(el, topic) {
    for (var p = el.parentElement; p && p !== topic; p = p.parentElement) if (p.tagName === "DETAILS" && !p.open) return true;
    return false;
  }
  function update() {
    var rt = 0, rb = window.innerHeight, boardVisible = true;
    if (root) { var rr = root.getBoundingClientRect(); rt = rr.top; rb = rr.bottom; boardVisible = !!(rr.width || rr.height); }
    var freeInfo = (host.hasAttribute && host.hasAttribute("data-board-free") && window.WsBoardAnchor && window.WsBoardAnchor.windowInfo) ? window.WsBoardAnchor.windowInfo() : null;
    if (freeInfo && !freeInfo.active) freeInfo = null;
    var above = [], below = [], topics = new Map();
    items.forEach(function (el) {
      var topic = el.closest("section.topic");
      if (topic && topic.hasAttribute && topic.hasAttribute("data-wb-offwindow")) {
        if (!boardVisible || topic.hidden || topic.hasAttribute("data-state-filtered") || folded(el, topic)) return;
        if (freeInfo) {
          var idx = freeInfo.indexOf(topic);
          if (idx >= 0 && idx < freeInfo.start) above.push(el);
          else if (idx > freeInfo.end) below.push(el);
        }
        return;
      }
      var tr = topic && topics.get(topic);
      if (topic && !tr) { tr = topic.getBoundingClientRect(); topics.set(topic, tr); }
      if (tr && (tr.bottom <= rt || tr.top >= rb)) {
        if ((tr.width || tr.height) && !folded(el, topic)) (tr.bottom <= rt ? above : below).push(el);
        return;
      }
      var r = el.getBoundingClientRect();
      if (!r.width && !r.height) return; // not rendered, e.g. the board tab is hidden
      if (r.bottom > rt && r.top < rb) return; // currently in view
      (r.top < rt ? above : below).push(el);
    });
    // Read reply clearance before either capsule writes.
    var reply = host.querySelector(".reply"), replyHeight = reply ? reply.offsetHeight : 0;
    var bottom = "calc(" + (replyHeight ? replyHeight + 18 : 10) + "px + var(--wb-rail-bot, 0px))";
    var topWidth = pills.top.offsetWidth, botWidth = pills.bot.offsetWidth, home = atTop();
    var topGoal = pills.top.wbMeasure ? pills.top.wbMeasure.offsetWidth : 0;
    var botGoal = pills.bot.wbMeasure ? pills.bot.wbMeasure.offsetWidth : 0;
    // Write phase: sync server-rendered holds (and clear any newly answered),
    // update topic header open/answered/held chips, then update pills.
    applyHoldDOM();
    syncTopicBars();
    show("top", above, topWidth, home, topGoal);
    show("bot", below, botWidth, home, botGoal);
    retitle();
    if (wraps.bot.style.bottom !== bottom) wraps.bot.style.bottom = bottom;
  }

  // Watch topic positions, not every far control's rendered visibility.
  // Counts always come from the data-backed items and one current layout.
  var io = new IntersectionObserver(redrawSoon, { root: root });
  positions.forEach(function (el) { io.observe(el); });

  // A jump scroll can carry an item past the view without a crossing the
  // observer reports, and a hidden board column changes size without one; a
  // scroll or resize refresh covers both, coalesced to one pending frame.
  function later() { redrawSoon(); }
  var scroller = root || window;
  scroller.addEventListener("scroll", later, { passive: true });
  window.addEventListener("resize", later);
  var ro = window.ResizeObserver ? new ResizeObserver(later) : null;
  if (ro) {
    ro.observe(host);
    // The host's border box need not resize when a mounted body changes
    // its topic's height. Refresh even if no topic crosses an IO threshold.
    positions.forEach(function (el) { ro.observe(el); });
  }

  // A pick, a ticked ack, or a typed note changes an item's draft state; the
  // host-level listeners run after form.js's own control handlers.
  // Typing redraws once a frame at most (redrawSoon): read right after
  // form.js grew the note, every item's rect forced a second layout per
  // keystroke.
  function redraw() { redrawSoon(); }
  var typed = 0;
  function redrawSoon() { if (!typed) typed = requestAnimationFrame(function () { typed = 0; update(); }); }
  function onHostClick(e) {
    var t = e && e.target;
    var btn = t && t.closest ? t.closest(".wb-hold-toggle") : null;
    if (btn && btn.hasAttribute && btn.hasAttribute("data-wb-hold")) {
      var item = btn.closest(".card.policy, .arow, .ckrow");
      if (item && items.indexOf(item) >= 0) {
        if (e.preventDefault) e.preventDefault();
        toggleHold(item);
        return;
      }
    }
    redraw();
  }
  // Listen on both #bcol and #bgal (docked gallery + Overview Quick Look stage)
  // so hold toggles, picks, notes, and bulk picks inside previewed or docked
  // topics update .tbar chips and Overview placeholders live.
  host.addEventListener("click", onHostClick);
  host.addEventListener("input", redrawSoon);
  host.addEventListener("change", redraw);
  var gal = d.getElementById ? d.getElementById("bgal") : null;
  var galHost = gal && gal !== host && (!host.contains || !host.contains(gal)) && gal.addEventListener ? gal : null;
  if (galHost) {
    galHost.addEventListener("click", onHostClick);
    galHost.addEventListener("input", redrawSoon);
    galHost.addEventListener("change", redraw);
  }
  // The dashboard's sent marks land after its region swaps, including a
  // conversation-only swap that does not re-run this script, so watch the
  // items' class attributes rather than wait for a board swap.
  var mo = window.MutationObserver ? new MutationObserver(redraw) : null;
  if (mo) items.forEach(function (el) { mo.observe(el, { attributes: true, attributeFilter: ["class"] }); });
  retitle();

  function click(edge) {
    return function activate(e) {
      if (edge === "top" && e.target.closest && e.target.closest(".oq-pull")) {
        if (st.apply) st.apply();
        return;
      }
      if (edge === "top" && e.target.closest && e.target.closest(".oq-arr, .oq-updates")) {
        if (st.home) st.home();
        else if (window.wbHistory && window.wbHistory.jump) window.wbHistory.jump(pills.top, { top: 0, behavior: "smooth" });
        else (root || window).scrollTo({ top: 0, behavior: window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "instant" : "smooth" });
        return;
      }
      var b = e.target.closest && e.target.closest(".oq-seg");
      var el = b && target[edge][b.getAttribute("data-k")];
      if (!el) return;
      if (st.apply) {
        if (!st.apply(function () { activate(e); })) return;
        if (el.isConnected === false) {
          // Applied history can put the old target back in an inert gap.
          var history = window.wbHistory;
          if (el.id && history && history.loadTarget && history.loadTarget(d, el.id, window.WsCards && window.WsCards.unfoldTopic)) return;
          el = el.id ? d.getElementById(el.id) : null;
        }
        if (!el) return;
      }
      var w = window.wbHistory;
      if (w && w.navigate) w.navigate(el, { behavior: "smooth" });
      else {
        if (window.WsCards && window.WsCards.activate) window.WsCards.activate(el);
        var anchor = window.WsBoardAnchor, card = anchor && anchor.owner && anchor.owner(el);
        var calm = !!(window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches);
        if (card) { anchor.select(card, { smooth: el === card && !calm }); if (el === card) return; }
        el.scrollIntoView({ block: "center", behavior: calm ? "instant" : "smooth" });
      }
    };
  }
  pills.top.onclick = click("top");
  pills.bot.onclick = click("bot");

  st.update = redrawSoon;
  st.syncTopicBars = syncTopicBars;
  st.isHeld = isHeldExternal;
  st.toggleHold = toggleHold;
  redrawSoon();
  st.off = function () {
    io.disconnect();
    if (ro) ro.disconnect();
    if (mo) mo.disconnect();
    if (typed) cancelAnimationFrame(typed);
    [pills.top, pills.bot].forEach(function (p) {
      if (p.wbGrow) p.wbGrow.cancel();
      if (p.wbMeasure) p.wbMeasure.parentNode.removeChild(p.wbMeasure);
      p.wbMeasure = null;
      p.style.width = "";
      if (p.style.overflow) p.style.overflow = "";
    });
    scroller.removeEventListener("scroll", later);
    window.removeEventListener("resize", later);
    host.removeEventListener("click", onHostClick);
    host.removeEventListener("input", redrawSoon);
    host.removeEventListener("change", redraw);
    if (galHost) {
      galHost.removeEventListener("click", onHostClick);
      galHost.removeEventListener("input", redrawSoon);
      galHost.removeEventListener("change", redraw);
    }
  };
})();
