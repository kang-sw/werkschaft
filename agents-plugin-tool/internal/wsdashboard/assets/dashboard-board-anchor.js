  // --- board card anchor & Designated-Y free-canvas virtual scroll ------------
  // The Main Board (#bcol) scrolls natively over a bounded free-canvas runway
  // (#r-board, position: relative) with only an active window of cards around
  // the current anchor positioned at absolute Y coordinates (radiating downward
  // below the anchor and upward above it). Cards keep their natural variable
  // heights and sticky headers; off-window ordinary topics take
  // data-wb-offwindow="hidden" + body eviction while exempt live cards park at
  // data-wb-offwindow="parked" + inert (never display: none).
  var ANCHOR_STORE = "ws.dashboard.board-card." + location.pathname + location.search;
  var ANCHOR_CAP = 17, ANCHOR_GAP = 3, ANCHOR_PAD = 4;
  var ANCHOR_BOT_0 = 6, ANCHOR_BOT_1 = 27, ANCHOR_BOT_2 = 47, ANCHOR_CARD_GAP_EXTRA = 12;
  var FREE_RUNWAY_MID = 50000;
  var FREE_RUNWAY_HEIGHT = 2 * FREE_RUNWAY_MID;
  var FREE_RUNWAY_MARGIN = 10000;
  var FREE_IDLE_RECENTER_MS = 150;
  var FREE_BASE_WINDOW = 3;
  var FREE_MAX_WINDOW = 8, FREE_EVICT_MARGIN = 4;
  var FREE_VIEWPORT_COVERAGE = 1.5;
  var FREE_CARD_GAP = 16;
  var FREE_FALLBACK_H = 320;
  var FREE_RESIZE_EPS = 0.5;
  var FREE_BOT_PAD = 48;
  var FREE_PIN_MS = 1500;
  var anchorKey = null, anchorSized = { w: 0, h: 0, bar: 0, reply: 0, padTop: 0, inset: 0, bounded: false };
  var anchorLast = null, anchorRO = null, anchorMO = null, anchorBoardMO = null, anchorFaceEl = null, anchorReplyEl = null, anchorCache = null;
  var anchorCap0 = null, anchorCapR0 = null, anchorCap1 = null, anchorBotNodes = [], anchorLastNode = null;
  var anchorProg = null, anchorProgSeq = 0, anchorUserScroll = 0;
  var freeActive = false, freeSuspended = false;
  var freeWinStart = 0, freeWinEnd = -1, freeAnchorIdx = 0;
  // Heights outlive an evicted frame in the shell (dashboard-session-view.js).
  var freePos = new Map(), freeLaidH = new Map(), freeHeightCache = typeof boardHeightCache === "function" ? boardHeightCache() : new Map();
  var freeCardRO = null, freeObserved = new Set(), freeParkedByUs = new WeakSet(), freeDeferredCollapse = new Set();
  var freeScrollRaf = 0, freeStageRaf = 0, freeIdleTimer = 0, freeJump = null, freeStagePending = false, freeTopRebasePending = false, freeScrollGen = 0, freeLastScrollTop = 0, freeLastShift = 0, freePendingScrollEnd = false;
  var freePin = null, freePinTimer = 0, freePinRaf = 0;

  // --- windowed O(1) topic body virtualization --------------------------------
  // Far section.topic bodies outside the active free-canvas window (or
  // ±virtualNearSlots when reconciled standalone) are detached into memory
  // while keeping their outer shell and .tophead mounted (intact Overview
  // tiles & #bdial). Strict exemptions: .bcard widgets, #bgal-docked panes,
  // Overview Quick Look previewed panels, maximized panels, .xterm/.tm-host
  // PTYs, and topics with active focus/edits/selection are never evicted.
  var VIRTUAL_NEAR_DEFAULT = 2;
  var virtualNearSlots = VIRTUAL_NEAR_DEFAULT;
  var virtualStore = new Map();
  var virtualNearSet = new Set();
  var virtualExemptSet = new Set();
  var virtualExemptDirty = new Set();
  var virtualInitialized = false;
  var virtualLastBounded = null;
  var virtualLastNearSlots = VIRTUAL_NEAR_DEFAULT;
  var virtualStats = { describes: 0, prunes: 0, fullReconciles: 0, deltaReconciles: 0, slotVisits: 0, frameIndexes: 0 };

  function virtualMarkExemptDirty(node) {
    if (!node || !virtualExemptSet.size) return;
    var sec = node.nodeType === 1 && node.matches && node.matches("section.topic[data-topic]")
      ? node
      : (node.closest ? node.closest("section.topic[data-topic]") : null);
    if (sec && virtualExemptSet.has(sec)) virtualExemptDirty.add(sec);
  }
  var VIRTUAL_ASK_SEL = '.card.policy[id^="item-"], .arow[id^="item-"], .ckrow[data-id]:not([data-assumed])';
  var VIRTUAL_KEEP_ATTRS = {
    "data-wb-virtual": true, "data-wb-offwindow": true, "data-current": true, "data-anchor-cap": true,
    "data-anchor-bot": true, "data-anchor-last": true, "data-state-filtered": true,
    "data-size": true, "tabindex": true, "data-overview-live": true,
    "data-max": true, "data-has-panel-max": true
  };

  function virtualIsItemExempt(it) {
    if (!it || !it.isTopic) return true;
    return !!(it.docked || it.overviewLive || it.maximized || it.hasTerminal || it.hasActiveEdit || it.hasSelection);
  }

  // Pure planner: returns per-slot action ("exempt" | "hydrate" | "collapse" |
  // "keep" | "keep-collapsed") for deterministic unit testing without DOM.
  function planBoardVirtualWindow(items, activeIdx, opts) {
    var cfg = opts || {};
    var near = typeof cfg.nearSlots === "number" && cfg.nearSlots >= 0 ? cfg.nearSlots : virtualNearSlots;
    var enabled = cfg.enabled !== false;
    var scrubbing = !!cfg.scrubbing;
    if (!Array.isArray(items) || !items.length) return { skip: false, actions: [] };
    if (scrubbing) return { skip: true, actions: [] };
    var cur = typeof activeIdx === "number" && activeIdx >= 0 && activeIdx < items.length ? activeIdx : 0;
    var actions = items.map(function (it, idx) {
      if (!it || !it.isTopic) return { index: idx, key: it && it.key || null, action: "exempt" };
      if (!enabled) return { index: idx, key: it.key, action: it.collapsed ? "hydrate" : "keep" };
      var inWindow = Math.abs(idx - cur) <= near;
      if (inWindow || virtualIsItemExempt(it)) {
        return { index: idx, key: it.key, action: it.collapsed ? "hydrate" : "keep" };
      }
      return { index: idx, key: it.key, action: it.collapsed ? "keep-collapsed" : "collapse" };
    });
    return { skip: false, actions: actions };
  }

  function virtualHasActiveEdit(sec) {
    if (!sec) return false;
    var act = doc.activeElement;
    if (act && act !== sec && sec.contains && sec.contains(act)) {
      var head = sec.querySelector && sec.querySelector(":scope > .tophead");
      if (!head || !head.contains(act)) return true;
      var tag = act.tagName;
      if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || act.isContentEditable) return true;
    }
    if (sec.querySelectorAll) {
      var tnotes = sec.querySelectorAll(".tnote[data-topic]");
      for (var t = 0; t < tnotes.length; t++) {
        var tn = tnotes[t];
        if (!tn.hidden || (tn.value && String(tn.value).trim())) return true;
      }
      var fields = sec.querySelectorAll("textarea, input[type='text'], input[type='search'], input:not([type])");
      for (var i = 0; i < fields.length; i++) {
        var f = fields[i];
        if (f.closest && f.closest(".tophead")) continue;
        if (f.value && String(f.value).trim()) return true;
      }
      if (sec.querySelector('.opt[data-opt][aria-pressed="true"], label.ack > input:checked, label.ck > input:checked, [data-no]')) return true;
    }
    return false;
  }

  function virtualHasHeldFrame(sec) {
    if (!sec || !sec.querySelectorAll) return false;
    var frames = sec.querySelectorAll("iframe.doc-frame");
    for (var i = 0; i < frames.length; i++) {
      var f = frames[i];
      if (f.wbNext || f.wbOut || f.wbHoldSelection || f === doc.activeElement || (typeof mpend !== "undefined" && mpend && mpend.frame === f)) return true;
    }
    return false;
  }

  function virtualDescribeNode(n) {
    virtualStats.describes++;
    var k = topicKey(n);
    if (k === null) return { key: anchorId(n), isTopic: false };
    var docked = !!(window.WsCards && window.WsCards.isDocked && window.WsCards.isDocked(n));
    var overviewLive = !!(n.hasAttribute("data-overview-live") || (n.closest && n.closest(".overview-preview-stage")));
    var maximized = !!(n.hasAttribute("data-max") || n.hasAttribute("data-has-panel-max") || (n.querySelector && n.querySelector("[data-max]")));
    var hasTerminal = !!(n.querySelector && n.querySelector(".xterm, .tm-host"));
    return {
      key: "t:" + k,
      isTopic: true,
      collapsed: n.hasAttribute("data-wb-virtual"),
      docked: docked,
      overviewLive: overviewLive,
      maximized: maximized,
      hasTerminal: hasTerminal,
      hasActiveEdit: virtualHasActiveEdit(n),
      hasSelection: virtualHasHeldFrame(n)
    };
  }

  function virtualCaptureOpen(sec) {
    if (!sec || !sec.querySelectorAll) return [];
    var head = sec.querySelector(":scope > .tophead");
    var out = [], list = sec.querySelectorAll("details");
    for (var i = 0; i < list.length; i++) {
      var d = list[i];
      if (head && head.contains(d)) continue;
      out.push({ key: d.getAttribute("data-key") || "", id: d.id || "", idx: out.length, open: !!d.open });
    }
    return out;
  }

  function virtualRestoreOpen(sec, saved, opts) {
    if (!sec || !saved || !saved.length || !sec.querySelectorAll) return;
    var stashed = !!(opts && opts.stashed);
    var head = sec.querySelector(":scope > .tophead");
    var byKey = Object.create(null), byId = Object.create(null), byIdx = Object.create(null);
    saved.forEach(function (s) {
      if (s.key) byKey[s.key] = s.open;
      else if (s.id) byId[s.id] = s.open;
      else byIdx[s.idx] = s.open;
    });
    var list = sec.querySelectorAll("details"), bodyIdx = 0;
    for (var i = 0; i < list.length; i++) {
      var d = list[i];
      if (head && head.contains(d)) continue;
      if (stashed && d.classList && (d.classList.contains("entry-panel") || d.classList.contains("system-run"))) {
        bodyIdx++;
        continue;
      }
      var k = d.getAttribute("data-key") || "", id = d.id || "";
      if (k && Object.prototype.hasOwnProperty.call(byKey, k)) d.open = !!byKey[k];
      else if (id && Object.prototype.hasOwnProperty.call(byId, id)) d.open = !!byId[id];
      else if (!stashed && !k && !id && Object.prototype.hasOwnProperty.call(byIdx, bodyIdx)) d.open = !!byIdx[bodyIdx];
      bodyIdx++;
    }
  }

  function virtualHasAskItem(node) {
    if (!node || node.nodeType !== 1) return false;
    if (node.matches && node.matches(VIRTUAL_ASK_SEL)) return true;
    return !!(node.querySelector && node.querySelector(VIRTUAL_ASK_SEL));
  }

  function virtualCaptureForCollapse(sec, cold, preScrollTop) {
    return {
      scrollTop: cold ? 0 : (typeof preScrollTop === "number" ? preScrollTop : (sec.scrollTop || 0)),
      history: window.wbHistory && window.wbHistory.capture ? window.wbHistory.capture(sec) : null,
      answers: typeof readAnswers === "function" ? readAnswers(sec) : null,
      frameH: typeof frameHeights === "function" ? frameHeights(sec) : null,
      openDetails: virtualCaptureOpen(sec)
    };
  }

  function virtualCollapse(sec, preDesc, opts) {
    if (!sec || sec.nodeType !== 1 || !sec.hasAttribute("data-topic") || sec.hasAttribute("data-wb-virtual")) return false;
    if (virtualIsItemExempt(preDesc || virtualDescribeNode(sec))) return false;
    var head = sec.querySelector && sec.querySelector(":scope > .tophead");
    if (!head) return false;
    var k = topicKey(sec);
    if (k === null) return false;
    // Capture scrollTop and state before unmountFrames/DOM detachment so a
    // collapse never interleaves DOM writes with a forced scrollTop reflow.
    var preScrollTop = opts && typeof opts.preScrollTop === "number" ? opts.preScrollTop : undefined;
    var snap = opts && opts.snapshot ? opts.snapshot : virtualCaptureForCollapse(sec, !!(opts && opts.cold), preScrollTop);
    if (typeof unmountFrames === "function") unmountFrames(sec);
    var raw = Array.prototype.slice.call(sec.childNodes || []);
    var children = [], detached = 0;
    for (var i = 0; i < raw.length; i++) {
      var c = raw[i];
      if (c === head) continue;
      children.push(c);
      if (!virtualHasAskItem(c)) {
        sec.removeChild(c);
        detached++;
      }
    }
    if (!detached) return false;
    virtualStore.set(k, {
      children: children,
      scrollTop: snap.scrollTop,
      history: snap.history,
      answers: snap.answers,
      frameH: snap.frameH,
      openDetails: snap.openDetails,
      stashed: null
    });
    if (typeof wbUpdateInvalidateVirtual === "function") wbUpdateInvalidateVirtual(k);
    sec.setAttribute("data-wb-virtual", "1");
    return true;
  }

  function virtualSyncAttrs(liveSec, incomingSec) {
    var keepClasses = ["overview-here", "flash", "wb-stuck"];
    var preserved = keepClasses.filter(function (c) { return liveSec.classList && liveSec.classList.contains(c); });
    liveSec.className = incomingSec.className;
    preserved.forEach(function (c) { if (liveSec.classList) liveSec.classList.add(c); });
    Array.prototype.slice.call(liveSec.attributes || []).forEach(function (a) {
      if (a.name === "class" || VIRTUAL_KEEP_ATTRS[a.name]) return;
      if (!incomingSec.hasAttribute(a.name)) liveSec.removeAttribute(a.name);
    });
    Array.prototype.slice.call(incomingSec.attributes || []).forEach(function (a) {
      if (a.name === "class" || VIRTUAL_KEEP_ATTRS[a.name]) return;
      if (liveSec.getAttribute(a.name) !== a.value) liveSec.setAttribute(a.name, a.value);
    });
  }

  function virtualIsNearNode(sec) {
    if (!sec || !anchorSized.bounded) return true;
    var scrubbing = typeof dialScrubbing !== "undefined" && !!dialScrubbing;
    if (!scrubbing && virtualNearSet.has(sec)) return true;
    var list = anchorCards();
    if (!list.length) return true;
    var idx = anchorCache && anchorCache.indexByNode.has(sec) ? anchorCache.indexByNode.get(sec) : list.indexOf(sec);
    if (idx < 0) return true;
    var cur = anchorCurrent() || list[0];
    var curIdx = cur && anchorCache && anchorCache.indexByNode.has(cur) ? anchorCache.indexByNode.get(cur) : (cur ? list.indexOf(cur) : 0);
    if (curIdx < 0) curIdx = 0;
    if (Math.abs(idx - curIdx) <= virtualNearSlots) return true;
    return virtualIsItemExempt(virtualDescribeNode(sec));
  }

  function virtualStashUpdate(liveSec, incomingSec) {
    if (!liveSec || !incomingSec || !liveSec.hasAttribute("data-wb-virtual")) return false;
    if (!anchorSized.bounded || virtualIsNearNode(liveSec)) return false;
    var k = topicKey(liveSec);
    if (k === null) return false;
    var newlyClosed = !!(liveSec.classList && !liveSec.classList.contains("closed") && incomingSec.classList && incomingSec.classList.contains("closed"));
    var rec = virtualStore.get(k) || {
      children: null, scrollTop: 0, history: null, answers: null, frameH: null, openDetails: null, stashed: null
    };
    if (newlyClosed) {
      if (typeof forgetOpen === "function") {
        forgetOpen(liveSec);
        forgetOpen(incomingSec);
        if (rec.stashed) forgetOpen(rec.stashed);
        if (rec.children) {
          for (var fi = 0; fi < rec.children.length; fi++) {
            if (rec.children[fi] && rec.children[fi].nodeType === 1) forgetOpen(rec.children[fi]);
          }
        }
      }
      rec.openDetails = null;
    }
    var oldHead = liveSec.querySelector && liveSec.querySelector(":scope > .tophead");
    var headAnswers = oldHead && typeof readAnswers === "function" ? readAnswers(oldHead) : null;
    virtualSyncAttrs(liveSec, incomingSec);
    var newHead = incomingSec.querySelector && incomingSec.querySelector(":scope > .tophead");
    if (oldHead && newHead) {
      liveSec.replaceChild(newHead.cloneNode(true), oldHead);
      if (headAnswers && Object.keys(headAnswers).length) {
        if (typeof writeAnswers === "function") writeAnswers(liveSec, headAnswers);
        if (typeof bindCapsuleNotes === "function") bindCapsuleNotes(liveSec);
        if (window.wbBindBoard) window.wbBindBoard(liveSec);
        else if (window.wbForm) window.wbForm();
      }
    }
    // Refresh any kept open-ask child without mounting heavy body parts.
    Array.prototype.slice.call(liveSec.childNodes || []).forEach(function (c) {
      if (c !== liveSec.querySelector(":scope > .tophead")) liveSec.removeChild(c);
    });
    Array.prototype.slice.call(incomingSec.childNodes || []).forEach(function (c) {
      if (c !== newHead && virtualHasAskItem(c)) liveSec.appendChild(c.cloneNode(true));
    });
    if (typeof fmtTimes === "function") fmtTimes(liveSec);
    rec.stashed = incomingSec.cloneNode(true);
    rec.children = null;
    virtualStore.set(k, rec);
    if (typeof wbUpdateInvalidateVirtual === "function") wbUpdateInvalidateVirtual(k);
    return true;
  }

  function virtualTakeCollapsed(k) {
    if (k === null || k === undefined) return null;
    var rec = virtualStore.get(k) || null;
    virtualStore.delete(k);
    if (typeof wbUpdateInvalidateVirtual === "function") wbUpdateInvalidateVirtual(k);
    return rec;
  }

  function virtualDropCollapsed(k) {
    if (k !== null && k !== undefined) {
      virtualStore.delete(k);
      if (typeof wbUpdateInvalidateVirtual === "function") wbUpdateInvalidateVirtual(k);
    }
  }

  function virtualHydrate(sec, opts) {
    if (typeof sec === "string") sec = virtualFindTopic(sec);
    if (!sec || !sec.hasAttribute || !sec.hasAttribute("data-wb-virtual")) return false;
    var k = topicKey(sec);
    var rec = k !== null ? virtualStore.get(k) : null;
    sec.removeAttribute("data-wb-virtual");
    if (k !== null) {
      virtualStore.delete(k);
      if (typeof wbUpdateInvalidateVirtual === "function") wbUpdateInvalidateVirtual(k);
    }
    if (!virtualNearSet.has(sec)) virtualExemptSet.add(sec);
    if (!rec) return false;
    var immediate = !!(opts && opts.immediate);
    var head = sec.querySelector && sec.querySelector(":scope > .tophead");
    if (rec.stashed) {
      Array.prototype.slice.call(sec.childNodes || []).forEach(function (c) {
        if (c !== head) sec.removeChild(c);
      });
      var stashedHead = rec.stashed.querySelector && rec.stashed.querySelector(":scope > .tophead");
      Array.prototype.slice.call(rec.stashed.childNodes || []).forEach(function (c) {
        if (c !== stashedHead) sec.appendChild(c);
      });
      if (window.wbHistory) {
        window.wbHistory.init(sec);
        if (rec.history) window.wbHistory.restore(sec, rec.history);
      }
      if (typeof watchBoard === "function") watchBoard(sec);
      if (typeof bandBoard === "function") bandBoard(sec);
      virtualRestoreOpen(sec, rec.openDetails, { stashed: true });
      if (typeof restoreOpen === "function") restoreOpen(sec);
      if (typeof fmtTimes === "function") fmtTimes(sec);
      if (typeof sweepOutImgs === "function") sweepOutImgs(sec);
      if (rec.frameH && typeof setFrameHeights === "function") setFrameHeights(sec, rec.frameH);
      if (immediate && typeof mountFrames === "function") mountFrames(sec);
      else if (typeof mountFramesPaced === "function") mountFramesPaced(sec);
      if (rec.answers && typeof writeAnswers === "function") writeAnswers(sec, rec.answers);
      if (window.WsCards && window.WsCards.decorate) window.WsCards.decorate(sec);
      if (typeof indexFrames === "function") indexFrames(sec);
      if (window.wbBindBoard) window.wbBindBoard(sec);
      else if (window.wbForm) window.wbForm();
      if (typeof bindCapsuleNotes === "function") bindCapsuleNotes(sec);
      if (typeof mermaid === "function") mermaid([sec]);
      if (typeof applySent === "function") applySent(true, sec);
    } else if (rec.children) {
      for (var i = 0; i < rec.children.length; i++) sec.appendChild(rec.children[i]);
      virtualRestoreOpen(sec, rec.openDetails);
      if (typeof bandBoard === "function") bandBoard(sec);
      if (immediate && typeof mountFrames === "function") mountFrames(sec);
      else if (typeof mountFramesPaced === "function") mountFramesPaced(sec);
      if (typeof indexFrames === "function") indexFrames(sec);
    }
    if (rec.scrollTop) sec.scrollTop = rec.scrollTop;
    return true;
  }

  function virtualNodeContainsId(node, id) {
    if (!node || (node.nodeType !== 1 && node.nodeType !== 11)) return false;
    if (node.nodeType === 1 && node.id === id) return true;
    var esc = window.CSS && window.CSS.escape ? window.CSS.escape(id) : String(id).replace(/["\\]/g, "\\$&");
    if (node.querySelector && node.querySelector("#" + esc)) return true;
    if (node.querySelectorAll) {
      var tpls = node.querySelectorAll("template");
      for (var i = 0; i < tpls.length; i++) {
        if (tpls[i].content && virtualNodeContainsId(tpls[i].content, id)) return true;
      }
    }
    return false;
  }

  function virtualNodeHasLazyGap(node) {
    if (!node || (node.nodeType !== 1 && node.nodeType !== 11)) return false;
    if (node.nodeType === 1 && node.hasAttribute && node.hasAttribute("data-lazy")) return true;
    return !!(node.querySelector && node.querySelector("[data-lazy]"));
  }

  function virtualFindTopic(key) {
    if (key === null || key === undefined) return null;
    var board = typeof region === "function" ? region("board") : null;
    if (board && board.querySelector) {
      var esc = window.CSS && window.CSS.escape ? window.CSS.escape(String(key)) : String(key).replace(/["\\]/g, "\\$&");
      var found = board.querySelector('section.topic[data-topic="' + esc + '"]');
      if (found) return found;
    }
    return anchorNode("t:" + key);
  }

  function virtualHydrateForTarget(id) {
    if (!id) return false;
    var live = $(id);
    if (live && live.hasAttribute && live.hasAttribute("data-wb-virtual")) {
      return virtualHydrate(live, { immediate: true });
    }
    if (live) return false;
    var foundKey = null;
    virtualStore.forEach(function (rec, k) {
      if (foundKey !== null) return;
      if (rec.stashed && virtualNodeContainsId(rec.stashed, id)) { foundKey = k; return; }
      if (rec.children) {
        for (var i = 0; i < rec.children.length; i++) {
          if (virtualNodeContainsId(rec.children[i], id)) { foundKey = k; return; }
        }
      }
    });
    if (foundKey === null) return false;
    var sec = virtualFindTopic(foundKey);
    return sec ? virtualHydrate(sec, { immediate: true }) : false;
  }

  function virtualHydrateLazyTopics() {
    var keys = [];
    virtualStore.forEach(function (rec, k) {
      if (rec.stashed && virtualNodeHasLazyGap(rec.stashed)) { keys.push(k); return; }
      if (rec.children) {
        for (var i = 0; i < rec.children.length; i++) {
          if (virtualNodeHasLazyGap(rec.children[i])) { keys.push(k); return; }
        }
      }
    });
    var hydrated = false;
    for (var j = 0; j < keys.length; j++) {
      var sec = virtualFindTopic(keys[j]);
      if (sec && virtualHydrate(sec, { immediate: true })) hydrated = true;
    }
    return hydrated;
  }

  function virtualSearchRoots(topicOrKey) {
    var k = typeof topicOrKey === "string" ? topicOrKey : topicKey(topicOrKey);
    if (k === null || k === undefined) return [];
    var rec = virtualStore.get(k);
    if (!rec) return [];
    if (rec.stashed) return [rec.stashed];
    if (rec.children) {
      return rec.children.filter(function (c) { return c && c.nodeType === 1; });
    }
    return [];
  }

  function virtualHydrateLazySearch() {
    if (!virtualStore.size) return Promise.resolve(0);
    if (!window.wbHistory || typeof window.wbHistory.hydrateDetached !== "function") return Promise.resolve(0);
    var names = [];
    virtualStore.forEach(function (rec, k) {
      if (rec.stashed && virtualNodeHasLazyGap(rec.stashed)) { names.push(k); return; }
      if (rec.children) {
        for (var i = 0; i < rec.children.length; i++) {
          if (virtualNodeHasLazyGap(rec.children[i])) { names.push(k); return; }
        }
      }
    });
    if (!names.length) return Promise.resolve(0);
    return window.wbHistory.hydrateDetached(virtualSearchRoots, names);
  }

  function virtualPrune(presentKeys) {
    virtualStats.prunes++;
    anchorCache = null;
    virtualNearSet.forEach(function (n) { if (!n || !n.isConnected) virtualNearSet.delete(n); });
    virtualExemptSet.forEach(function (n) { if (!n || !n.isConnected) virtualExemptSet.delete(n); });
    virtualExemptDirty.forEach(function (n) { if (!n || !n.isConnected || !virtualExemptSet.has(n)) virtualExemptDirty.delete(n); });
    if (!virtualStore.size) return 0;
    var present;
    if (presentKeys instanceof Set) {
      present = presentKeys;
    } else if (Array.isArray(presentKeys)) {
      present = new Set(presentKeys);
    } else {
      present = new Set();
      var board = typeof region === "function" ? region("board") : null;
      if (board && board.querySelectorAll) {
        var allBoardTopics = board.querySelectorAll("section.topic[data-topic]");
        for (var bi = 0; bi < allBoardTopics.length; bi++) {
          var btk = topicKey(allBoardTopics[bi]);
          if (btk !== null) present.add(btk);
        }
      }
      var list = anchorCards(true);
      for (var i = 0; i < list.length; i++) {
        var tk = topicKey(list[i]);
        if (tk !== null) present.add(tk);
      }
    }
    if (doc.querySelectorAll) {
      var stageTopics = doc.querySelectorAll(".overview-preview-stage > section.topic[data-topic]");
      for (var si = 0; si < stageTopics.length; si++) {
        var stk = topicKey(stageTopics[si]);
        if (stk !== null) present.add(stk);
      }
    }
    var pruned = 0;
    Array.from(virtualStore.keys()).forEach(function (k) {
      if (!present.has(k)) {
        virtualStore.delete(k);
        if (typeof wbUpdateInvalidateVirtual === "function") wbUpdateInvalidateVirtual(k);
        pruned++;
      }
    });
    return pruned;
  }

  function virtualReconcile(opts) {
    var scrubbing = typeof dialScrubbing !== "undefined" && !!dialScrubbing && !(opts && opts.force);
    if (scrubbing) return false;
    var bounded = !!anchorSized.bounded;
    var full = !!(opts && opts.full) || !virtualInitialized || virtualLastBounded !== bounded || virtualLastNearSlots !== virtualNearSlots;
    var list = anchorCards(full);
    var cur = anchorCurrent() || list[0] || null;
    var activeIdx = cur && anchorCache && anchorCache.indexByNode.has(cur) ? anchorCache.indexByNode.get(cur) : (cur ? list.indexOf(cur) : 0);
    if (activeIdx < 0) activeIdx = 0;
    var changed = false;

    if (!bounded) {
      virtualInitialized = true;
      virtualLastBounded = false;
      virtualLastNearSlots = virtualNearSlots;
      virtualNearSet.clear();
      virtualExemptSet.clear();
      virtualExemptDirty.clear();
      if (virtualStore.size) {
        Array.from(virtualStore.keys()).forEach(function (k) {
          var sec = virtualFindTopic(k);
          if (sec && virtualHydrate(sec, { immediate: sec === cur })) changed = true;
        });
      }
      if (opts && opts.full) virtualPrune();
      return changed;
    }

    var lo, hi;
    if (opts && opts.window && typeof opts.window.start === "number" && typeof opts.window.end === "number") {
      lo = list.length ? Math.max(0, Math.min(list.length - 1, opts.window.start)) : 0;
      hi = list.length ? Math.max(-1, Math.min(list.length - 1, opts.window.end)) : -1;
    } else if (typeof freeActive !== "undefined" && freeActive && freeWinEnd >= freeWinStart && list.length) {
      lo = Math.max(0, Math.min(list.length - 1, freeWinStart));
      hi = Math.min(list.length - 1, freeWinEnd);
    } else {
      lo = list.length ? Math.max(0, activeIdx - virtualNearSlots) : 0;
      hi = list.length ? Math.min(list.length - 1, activeIdx + virtualNearSlots) : -1;
    }
    var nextNear = new Set();
    for (var ni = lo; ni <= hi; ni++) {
      virtualStats.slotVisits++;
      nextNear.add(list[ni]);
    }

    if (full) {
      virtualStats.fullReconciles++;
      virtualInitialized = true;
      virtualLastBounded = true;
      virtualLastNearSlots = virtualNearSlots;
      var items = list.map(virtualDescribeNode);
      var useExplicitWindow = !!(opts && opts.window) || (typeof freeActive !== "undefined" && freeActive && freeWinEnd >= freeWinStart);
      var plan;
      if (useExplicitWindow) {
        plan = {
          skip: false,
          actions: items.map(function (it, idx) {
            if (!it || !it.isTopic) return { index: idx, key: it && it.key || null, action: "exempt" };
            var inWindow = idx >= lo && idx <= hi;
            if (inWindow || virtualIsItemExempt(it)) return { index: idx, key: it.key, action: it.collapsed ? "hydrate" : "keep" };
            return { index: idx, key: it.key, action: it.collapsed ? "keep-collapsed" : "collapse" };
          })
        };
      } else {
        plan = planBoardVirtualWindow(items, activeIdx, {
          nearSlots: virtualNearSlots,
          enabled: true,
          scrubbing: false
        });
      }
      if (plan.skip) return false;
      virtualNearSet = nextNear;
      virtualExemptSet = new Set();
      virtualExemptDirty = new Set();
      // Hydrate newly near or newly exempt topics first, then collapse far ones.
      for (var h = 0; h < plan.actions.length; h++) {
        if (plan.actions[h].action === "hydrate") {
          if (virtualHydrate(list[h], { immediate: h === activeIdx })) changed = true;
        }
        if (!nextNear.has(list[h]) && items[h] && items[h].isTopic && virtualIsItemExempt(items[h])) {
          virtualExemptSet.add(list[h]);
        }
      }
      var preScrollTops = opts && opts.preScrollTops;
      for (var c = 0; c < plan.actions.length; c++) {
        if (plan.actions[c].action === "collapse") {
          var preTop = preScrollTops && preScrollTops.has(list[c]) ? preScrollTops.get(list[c]) : undefined;
          if (virtualCollapse(list[c], items[c], typeof preTop === "number" ? { preScrollTop: preTop } : undefined)) changed = true;
        }
      }
    } else {
      // Steady-state O(1) delta reconcile: inspect ONLY leaving slots and
      // previously-exempt far topics whose exemption state changed (dirty);
      // never revisit unchanged far exempt topics or prune virtualStore during
      // normal navigation.
      virtualStats.deltaReconciles++;
      var candidates = [];
      virtualNearSet.forEach(function (prevNode) {
        virtualStats.slotVisits++;
        if (!prevNode || !prevNode.isConnected || nextNear.has(prevNode)) return;
        if (prevNode.hasAttribute && prevNode.hasAttribute("data-wb-virtual")) return;
        candidates.push(prevNode);
      });
      if (virtualExemptDirty.size) {
        Array.from(virtualExemptDirty).forEach(function (exNode) {
          virtualExemptDirty.delete(exNode);
          virtualStats.slotVisits++;
          if (!exNode || !exNode.isConnected || nextNear.has(exNode)) {
            virtualExemptSet.delete(exNode);
            return;
          }
          if (virtualExemptSet.has(exNode) && candidates.indexOf(exNode) < 0) candidates.push(exNode);
        });
      }
      if (freeDeferredCollapse.size) {
        Array.from(freeDeferredCollapse).forEach(function (dn) {
          freeDeferredCollapse.delete(dn);
          virtualStats.slotVisits++;
          if (!dn || !dn.isConnected || nextNear.has(dn)) return;
          if (dn.hasAttribute && dn.hasAttribute("data-wb-virtual")) return;
          if (candidates.indexOf(dn) < 0) candidates.push(dn);
        });
      }
      virtualNearSet = nextNear;
      var entering = [];
      for (var ei = lo; ei <= hi; ei++) {
        var en = list[ei];
        virtualExemptSet.delete(en);
        virtualExemptDirty.delete(en);
        if (en && en.hasAttribute && en.hasAttribute("data-wb-virtual")) {
          entering.push({ node: en, immediate: ei === activeIdx });
        }
      }
      for (var eh = 0; eh < entering.length; eh++) {
        if (virtualHydrate(entering[eh].node, { immediate: entering[eh].immediate })) changed = true;
      }
      var preScrollTopsDelta = opts && opts.preScrollTops;
      for (var ci = 0; ci < candidates.length; ci++) {
        var cn = candidates[ci];
        virtualExemptDirty.delete(cn);
        var desc = virtualDescribeNode(cn);
        if (!desc.isTopic) {
          virtualExemptSet.delete(cn);
          continue;
        }
        if (virtualIsItemExempt(desc)) {
          virtualExemptSet.add(cn);
          if (desc.collapsed && virtualHydrate(cn, { immediate: false })) changed = true;
        } else {
          virtualExemptSet.delete(cn);
          var preTopDelta = preScrollTopsDelta && preScrollTopsDelta.has(cn) ? preScrollTopsDelta.get(cn) : undefined;
          if (!desc.collapsed && virtualCollapse(cn, desc, typeof preTopDelta === "number" ? { preScrollTop: preTopDelta } : undefined)) changed = true;
        }
      }
    }

    // Ensure any Overview Quick Look previewed node outside #r-board is hydrated.
    var livePreview = doc.querySelector && doc.querySelector(".overview-preview-stage > section.topic[data-wb-virtual]");
    if (livePreview) {
      if (virtualHydrate(livePreview, { immediate: true })) changed = true;
    }
    if (opts && opts.prune) virtualPrune();
    return changed;
  }

  window.WsBoardVirtual = {
    nearSlots: function (n) {
      if (typeof n === "number" && n >= 0) { virtualNearSlots = Math.floor(n); virtualReconcile({ full: true }); }
      return virtualNearSlots;
    },
    plan: planBoardVirtualWindow,
    isExempt: virtualIsItemExempt,
    isNear: virtualIsNearNode,
    hydrate: virtualHydrate,
    hydrateForTarget: virtualHydrateForTarget,
    collapse: virtualCollapse,
    reconcile: virtualReconcile,
    markExemptDirty: virtualMarkExemptDirty,
    prune: virtualPrune,
    searchRoots: virtualSearchRoots,
    detachedNodes: virtualSearchRoots,
    hydrateLazySearch: virtualHydrateLazySearch,
    stats: function () { return Object.assign({}, virtualStats); },
    resetStats: function () {
      virtualStats.describes = 0;
      virtualStats.prunes = 0;
      virtualStats.fullReconciles = 0;
      virtualStats.deltaReconciles = 0;
      virtualStats.slotVisits = 0;
      virtualStats.frameIndexes = 0;
    },
    hasStashed: function (k) { var r = virtualStore.get(k); return !!(r && r.stashed); },
    collapsedCount: function () { return virtualStore.size; }
  };
  function anchorCapH(n) { return n ? ANCHOR_PAD + n * (ANCHOR_CAP + ANCHOR_GAP) : 0; }
  function anchorToolbarFace() {
    var bar = window.WsCards && window.WsCards.toolbar && window.WsCards.toolbar();
    return bar && (bar.firstElementChild || bar) || null;
  }
  function anchorRailInset(col) {
    if (col && col.style && col.style.getPropertyValue) {
      var raw = parseFloat(col.style.getPropertyValue("--wb-rail-inset"));
      if (!isNaN(raw)) return raw;
    }
    return anchorSized.bar ? anchorSized.bar + 6 : anchorSized.inset;
  }
  function anchorBotReserve(n) {
    var base = n === 0 ? ANCHOR_BOT_0 : n === 1 ? ANCHOR_BOT_1 : ANCHOR_BOT_2;
    return base + (anchorSized.reply ? anchorSized.reply + 14 : 0);
  }
  function anchorCardCap(n, list) {
    if (!n || !(n.hasAttribute("data-topic") || n.hasAttribute("data-rail"))) return 0;
    if (!list || (anchorCache && list === anchorCache.list)) {
      anchorCards();
      var idx = anchorCache.indexByNode.get(n);
      if (idx !== undefined) return anchorCapH(Math.min(2, anchorCache.railBefore[idx]));
    }
    var cards = list || anchorCards(), at = cards.indexOf(n), prev = 0;
    for (var i = 0; i < at; i++) if (cards[i].hasAttribute("data-topic") || cards[i].hasAttribute("data-rail")) prev++;
    return anchorCapH(Math.min(2, prev));
  }
  function anchorIsBounded(col) {
    if (anchorSized.bounded) return true;
    if (col && typeof getComputedStyle === "function") {
      try { return /auto|scroll/.test(getComputedStyle(col).overflowY); } catch (e) {}
    }
    return false;
  }
  function anchorScroller(col, bounded) {
    return bounded ? col : (doc.scrollingElement || doc.documentElement || col);
  }
  function anchorLandingTop(col, colRect, n, list) {
    var top = colRect ? colRect.top + (col.clientTop || 0) : 0;
    return top + anchorSized.padTop + anchorRailInset(col) + anchorCardCap(n, list);
  }
  function anchorId(n) {
    var k = n && n.nodeType === 1 ? topicKey(n) : null;
    return k !== null ? "t:" + k : n && n.nodeType === 1 && n.hasAttribute("data-card") ? "c:" + n.getAttribute("data-card") : null;
  }
  // The canonical card order: the board region's cards in board order, fetched
  // closed pages (nested in one another) in place, filtered ones left out.
  // Docked topics stand in the gallery and have their own pages. Maintained as
  // a keyed cache rebuilt only on structural updates (settle, board mutations,
  // full reconcile) so steady-state navigation stays strictly O(1).
  function anchorRecordsAffectOrder(records) {
    if (!records || !records.length) return false;
    var b = region("board");
    for (var i = 0; i < records.length; i++) {
      var r = records[i], t = r.target;
      if (!t) continue;
      if (r.type === "childList") {
        if (t === b || (t.classList && t.classList.contains("closed-page"))) return true;
      } else if (r.type === "attributes") {
        var p = t.parentNode;
        if (p && (p === b || (p.classList && p.classList.contains("closed-page")))) return true;
      }
    }
    return false;
  }
  function anchorProcessRecords(records) {
    if (!records || !records.length) return;
    if (freePin) freePinLayout();
    if (anchorRecordsAffectOrder(records)) anchorCache = null;
    if (virtualExemptSet.size) {
      for (var i = 0; i < records.length; i++) {
        virtualMarkExemptDirty(records[i].target);
      }
    }
  }
  function anchorDrainMutations() {
    if (anchorBoardMO && anchorBoardMO.takeRecords) {
      anchorProcessRecords(anchorBoardMO.takeRecords());
    }
  }
  function anchorCards(rebuild) {
    if (!rebuild && typeof anchorDrainMutations === "function") anchorDrainMutations();
    if (!rebuild && anchorCache) return anchorCache.list;
    var b = region("board"), out = [], byKey = new Map(), indexByNode = new Map(), railBefore = [], railList = [], railCount = 0;
    if (b) {
      (function walk(box) {
        Array.prototype.forEach.call(box.children, function (n) {
          if (n.classList.contains("closed-page")) walk(n);
          else {
            var id = anchorId(n);
            if (id !== null && !n.hidden && !n.hasAttribute("data-state-filtered")) {
              if (typeof virtualStats !== "undefined") virtualStats.slotVisits++;
              indexByNode.set(n, out.length);
              byKey.set(id, n);
              railBefore.push(railCount);
              if (n.hasAttribute("data-topic") || n.hasAttribute("data-rail")) {
                railList.push(n);
                railCount++;
              }
              out.push(n);
            }
          }
        });
      })(b);
    }
    anchorCache = { list: out, byKey: byKey, indexByNode: indexByNode, railBefore: railBefore, railList: railList, railCount: railCount };
    return out;
  }
  function anchorNode(key) {
    if (key === null) return null;
    anchorCards();
    if (typeof virtualStats !== "undefined") virtualStats.slotVisits++;
    return anchorCache.byKey.get(key) || null;
  }
  function anchorCurrent() {
    if (anchorKey === null) return null;
    if (typeof anchorDrainMutations === "function") anchorDrainMutations();
    if (anchorLast && anchorCache && anchorCache.indexByNode.has(anchorLast) && anchorLast.isConnected !== false && !anchorLast.hidden && !anchorLast.hasAttribute("data-state-filtered") && anchorId(anchorLast) === anchorKey) {
      if (typeof virtualStats !== "undefined") virtualStats.slotVisits++;
      return anchorLast;
    }
    return anchorNode(anchorKey);
  }
  function anchorStore() {
    try {
      if (anchorKey === null) window.sessionStorage.removeItem(ANCHOR_STORE);
      else window.sessionStorage.setItem(ANCHOR_STORE, anchorKey);
    } catch (e) {}
  }
  function anchorMark(n, opts) {
    var was = anchorLast;
    anchorLast = n || null;
    if (was && was !== n) was.removeAttribute("data-current");
    if (n) n.setAttribute("data-current", "");
    anchorKey = n ? anchorId(n) : null;
    anchorStore();
    if (typeof boardDialMark === "function") boardDialMark(n);
    if (!(opts && opts.skipVirtual) && typeof virtualReconcile === "function" && (typeof dialScrubbing === "undefined" || !dialScrubbing)) {
      virtualReconcile(opts && opts.full ? { full: true } : undefined);
    }
  }

  // --- Designated-Y free-canvas virtual scroll engine -------------------------
  function freeEnabled(col, list) {
    if (freeSuspended || !anchorSized.bounded || !col || !list || !list.length) return false;
    if (doc.documentElement && doc.documentElement.hasAttribute("data-overview")) return false;
    return true;
  }
  function freeTopPad(board) {
    if (!board) return 0;
    var tb = window.WsCards && window.WsCards.toolbar && window.WsCards.toolbar();
    if (!tb || !board.contains || !board.contains(tb)) {
      tb = board.querySelector ? board.querySelector(".board-toolbar") : null;
    }
    if (!tb || tb.hidden) return 0;
    var h = tb.offsetHeight || (tb.getBoundingClientRect ? tb.getBoundingClientRect().height : 0);
    return h > 0 ? Math.ceil(h) + FREE_CARD_GAP : 0;
  }
  function freeMeasureHeight(n) {
    if (!n) return FREE_FALLBACK_H;
    // A maximized sheet is position: fixed at viewport height; never let its
    // modal box overwrite the card's laid/cached board height.
    if (n.hasAttribute && n.hasAttribute("data-max")) return freeLaidHeight(n);
    var key = anchorId(n), h = 0;
    var pageHiddenNow = !!(doc && ((typeof docHidden === "function" ? docHidden() : doc.hidden) || (doc.documentElement && doc.documentElement.hasAttribute && doc.documentElement.hasAttribute("data-animation-paused")))) ||
      (typeof shHidden !== "undefined" && !!shHidden);
    if (pageHiddenNow) {
      if (typeof freeHeightCache !== "undefined") freeHeightCache._skippedHidden = true;
      if (key !== null && freeHeightCache.has(key)) return freeHeightCache.get(key);
      return FREE_FALLBACK_H;
    }
    if (!n.hasAttribute || !n.hasAttribute("data-wb-offwindow")) {
      h = n.offsetHeight || 0;
      if (!(h > 0) && n.getBoundingClientRect) {
        var r = n.getBoundingClientRect();
        h = r && r.height > 0 ? r.height : 0;
      }
    }
    if (h > 0) {
      if (key !== null) freeHeightCache.set(key, h);
      return h;
    }
    if (key !== null && freeHeightCache.has(key)) return freeHeightCache.get(key);
    return FREE_FALLBACK_H;
  }
  function freeCachedHeight(n) {
    var key = anchorId(n);
    if (key !== null && freeHeightCache.has(key)) return freeHeightCache.get(key);
    return FREE_FALLBACK_H;
  }
  function freeLaidHeight(n) {
    if (freeLaidH.has(n)) return freeLaidH.get(n);
    return freeCachedHeight(n);
  }
  function freeSetOffWindow(n, mode) {
    if (!n || n.nodeType !== 1) return;
    if (mode === null) {
      if (n.hasAttribute && n.hasAttribute("data-wb-offwindow")) n.removeAttribute("data-wb-offwindow");
      if (freeParkedByUs.has(n)) {
        n.inert = false;
        if (n.removeAttribute) n.removeAttribute("inert");
        freeParkedByUs.delete(n);
      }
      return;
    }
    if (freePin && freePin.card === n) freeReleasePin();
    if (mode === "hidden") {
      if (!n.getAttribute || n.getAttribute("data-wb-offwindow") !== "hidden") n.setAttribute("data-wb-offwindow", "hidden");
      if (freeParkedByUs.has(n)) {
        n.inert = false;
        if (n.removeAttribute) n.removeAttribute("inert");
        freeParkedByUs.delete(n);
      }
      return;
    }
    if (mode === "parked") {
      if (!n.getAttribute || n.getAttribute("data-wb-offwindow") !== "parked") n.setAttribute("data-wb-offwindow", "parked");
      if (!n.inert) {
        n.inert = true;
        if (n.setAttribute) n.setAttribute("inert", "");
        freeParkedByUs.add(n);
      }
    }
  }
  function freeClearNode(n, opts) {
    if (!n || n.nodeType !== 1) return;
    if (freePin && freePin.card === n) freeReleasePin();
    freeSetOffWindow(n, null);
    freeDeferredCollapse.delete(n);
    if (n.style && n.style.top) n.style.top = "";
    if (freeCardRO && freeObserved.has(n)) {
      try { freeCardRO.unobserve(n); } catch (e) {}
      freeObserved.delete(n);
    }
    if (!(opts && opts.keepCoords)) {
      freePos.delete(n);
      freeLaidH.delete(n);
    }
  }
  function freeCancelTimers() {
    var wasScrolling = !!(freeScrollRaf || freeIdleTimer);
    if (freeScrollRaf && typeof cancelAnimationFrame === "function") cancelAnimationFrame(freeScrollRaf);
    freeScrollRaf = 0;
    if (freeIdleTimer) clearTimeout(freeIdleTimer);
    freeIdleTimer = 0;
    freePendingScrollEnd = false;
    if (wasScrolling && typeof freeNotifyScrollIdle === "function") freeNotifyScrollIdle();
  }
  function freeNotifyScrollIdle() {
    if (doc && doc.dispatchEvent && typeof CustomEvent === "function") {
      doc.dispatchEvent(new CustomEvent("ws-board-scroll-idle"));
    }
    try {
      if (window.parent && window.parent !== window && typeof window.parent.wsBoardScrollIdle === "function") {
        window.parent.wsBoardScrollIdle();
      }
    } catch (_) {}
  }
  function freeSuspend() {
    freeReleasePin();
    freeJump = null;
    freeStagePending = false;
    freeTopRebasePending = false;
    freeCancelTimers();
    freeDeferredCollapse.clear();
    if (freeCardRO) {
      freeObserved.forEach(function (n) { try { freeCardRO.unobserve(n); } catch (e) {} });
      freeObserved.clear();
    }
    var col = $("bcol"), board = region("board");
    if (col && col.removeAttribute && col.hasAttribute && col.hasAttribute("data-board-free")) {
      col.removeAttribute("data-board-free");
    }
    if (board && board.style && board.style.minHeight) board.style.minHeight = "";
    var list = anchorCards();
    for (var i = 0; i < list.length; i++) freeClearNode(list[i]);
    freePos.forEach(function (_, n) { freeClearNode(n); });
    freePos.clear();
    freeLaidH.clear();
    if (board && board.querySelector) {
      var moreBtn = board.querySelector("button.closed-more");
      if (moreBtn) {
        if (moreBtn.removeAttribute) moreBtn.removeAttribute("data-wb-offwindow");
        if (moreBtn.style && moreBtn.style.top) moreBtn.style.top = "";
      }
    }
    if (typeof freeStageRaf !== "undefined" && freeStageRaf) {
      if (typeof cancelAnimationFrame === "function") cancelAnimationFrame(freeStageRaf);
      freeStageRaf = 0;
    }
    freeActive = false;
    freeWinStart = 0;
    freeWinEnd = -1;
  }
  function freeSuspendForOverview() {
    freeSuspended = true;
    freeSuspend();
  }
  function freeResumeFromOverview() {
    freeSuspended = false;
  }
  function freeComputeWindow(list, anchorIdx, viewH) {
    var n = list.length;
    if (!n) return { start: 0, end: -1 };
    var a = Math.max(0, Math.min(n - 1, anchorIdx));
    var need = Math.max(1, viewH) * FREE_VIEWPORT_COVERAGE;
    var start = Math.max(0, a - FREE_BASE_WINDOW);
    var end = Math.min(n - 1, a + FREE_BASE_WINDOW);
    var aboveH = 0;
    for (var i = a - 1; i >= start; i--) aboveH += freeLaidHeight(list[i]) + FREE_CARD_GAP;
    while (start > 0 && (a - start) < FREE_MAX_WINDOW && aboveH < need) {
      start--;
      aboveH += freeLaidHeight(list[start]) + FREE_CARD_GAP;
    }
    var belowH = 0;
    for (var j = a + 1; j <= end; j++) belowH += freeLaidHeight(list[j]) + FREE_CARD_GAP;
    while (end < n - 1 && (end - a) < FREE_MAX_WINDOW && belowH < need) {
      end++;
      belowH += freeLaidHeight(list[end]) + FREE_CARD_GAP;
    }
    return { start: start, end: end };
  }
  function freeSyncRO(list, start, end) {
    if (!window.ResizeObserver) return;
    if (!freeCardRO) freeCardRO = new ResizeObserver(freeOnCardResize);
    var want = new Set();
    for (var i = start; i <= end; i++) {
      var n = list[i];
      if (n && (!n.hasAttribute || (!n.hasAttribute("data-wb-offwindow") && !n.hasAttribute("data-max")))) want.add(n);
    }
    freeObserved.forEach(function (n) {
      if (!want.has(n)) {
        try { freeCardRO.unobserve(n); } catch (e) {}
        freeObserved.delete(n);
      }
    });
    want.forEach(function (n) {
      if (!freeObserved.has(n)) {
        freeCardRO.observe(n);
        freeObserved.add(n);
      }
    });
  }
  function freeFindTopVisibleIdx(list, col, viewTopY) {
    if (!list.length || freeWinEnd < freeWinStart) return Math.max(0, Math.min(list.length - 1, freeAnchorIdx));
    var s = Math.max(0, freeWinStart), e = Math.min(list.length - 1, freeWinEnd);
    var best = -1, closest = s, closestDist = Infinity;
    for (var i = s; i <= e; i++) {
      var n = list[i], y = freePos.get(n);
      if (y === undefined) continue;
      var h = freeLaidHeight(n);
      if (y + h > viewTopY + 0.5) {
        best = i;
        break;
      }
      var d = Math.abs(y + h - viewTopY);
      if (d < closestDist) { closestDist = d; closest = i; }
    }
    return best >= 0 ? best : closest;
  }
  function freeApplyOffWindow(nodeOrNodes, scrubbing, opts) {
    var nodes = Array.isArray(nodeOrNodes) ? nodeOrNodes : (nodeOrNodes ? [nodeOrNodes] : []);
    if (!nodes.length) return;
    opts = opts || {};
    var scrub = !!(scrubbing || opts.scrubbing);
    var cold = !!opts.cold;
    var deferCheck = typeof opts.deferCheck === "function" ? opts.deferCheck : null;
    var seen = new Set();
    var plan = [];
    // Pass 1 (READ): describe every candidate and capture scrollTop/frame state
    // before any off-window attribute write or DOM detachment dirties layout.
    for (var i = 0; i < nodes.length; i++) {
      var node = nodes[i];
      if (!node || seen.has(node)) continue;
      seen.add(node);
      // Parking a panel's topic would also hide and disable its fixed sheet.
      if (node.hasAttribute && (node.hasAttribute("data-max") || node.hasAttribute("data-has-panel-max"))) continue;
      var desc = virtualDescribeNode(node);
      var exempt = virtualIsItemExempt(desc);
      var defer = !exempt && !desc.collapsed && !!(scrub || (deferCheck && deferCheck(node)));
      var snap = null;
      if (!exempt && !desc.collapsed && !defer && node.nodeType === 1 && node.hasAttribute &&
          node.hasAttribute("data-topic") && !node.hasAttribute("data-wb-virtual") &&
          node.querySelector && node.querySelector(":scope > .tophead") && topicKey(node) !== null) {
        snap = typeof virtualCaptureForCollapse === "function"
          ? virtualCaptureForCollapse(node, cold)
          : {
              scrollTop: cold ? 0 : (node.scrollTop || 0),
              history: window.wbHistory && window.wbHistory.capture ? window.wbHistory.capture(node) : null,
              answers: typeof readAnswers === "function" ? readAnswers(node) : null,
              frameH: typeof frameHeights === "function" ? frameHeights(node) : null,
              openDetails: typeof virtualCaptureOpen === "function" ? virtualCaptureOpen(node) : []
            };
      }
      plan.push({ node: node, desc: desc, exempt: exempt, defer: defer, snap: snap });
    }
    // Pass 2 (WRITE): apply off-window state, unobserve RO, and collapse.
    for (var j = 0; j < plan.length; j++) {
      var p = plan[j], n = p.node;
      if (freeCardRO && freeObserved.has(n)) {
        try { freeCardRO.unobserve(n); } catch (e) {}
        freeObserved.delete(n);
      }
      freePos.delete(n);
      freeLaidH.delete(n);
      if (p.exempt) {
        freeSetOffWindow(n, "parked");
        if (p.desc.isTopic) virtualExemptSet.add(n);
      } else {
        freeSetOffWindow(n, "hidden");
        virtualExemptSet.delete(n);
        if (!p.desc.collapsed) {
          if (p.defer) freeDeferredCollapse.add(n);
          else {
            freeDeferredCollapse.delete(n);
            if (p.snap) virtualCollapse(n, p.desc, { snapshot: p.snap, cold: cold });
          }
        }
      }
    }
  }
  function freeUpdateMoreAndStage(board, list, winEnd, yEnd, hEnd, keepRunway) {
    if (typeof freeStageRaf !== "undefined" && freeStageRaf) {
      if (typeof cancelAnimationFrame === "function") cancelAnimationFrame(freeStageRaf);
      freeStageRaf = 0;
    }
    var moreBtn = board && board.querySelector ? board.querySelector("button.closed-more[data-closed-next]:not([hidden]):not([data-state-filtered])") : null;
    var stageH = FREE_RUNWAY_HEIGHT;
    if (winEnd === list.length - 1) {
      var lastBottom = yEnd + hEnd;
      if (moreBtn) {
        if (moreBtn.hasAttribute && moreBtn.hasAttribute("data-wb-offwindow")) moreBtn.removeAttribute("data-wb-offwindow");
        var moreTop = Math.round(lastBottom + FREE_CARD_GAP);
        if (moreBtn.style.top !== moreTop + "px") moreBtn.style.top = moreTop + "px";
        var moreH = moreBtn.offsetHeight || (moreBtn.getBoundingClientRect ? moreBtn.getBoundingClientRect().height : 0) || 36;
        lastBottom = moreTop + moreH;
      }
      stageH = Math.ceil(lastBottom + FREE_BOT_PAD);
    } else {
      if (moreBtn) {
        if (!moreBtn.getAttribute || moreBtn.getAttribute("data-wb-offwindow") !== "hidden") moreBtn.setAttribute("data-wb-offwindow", "hidden");
        if (moreBtn.style.top) moreBtn.style.top = "";
      }
      stageH = Math.max(FREE_RUNWAY_HEIGHT, Math.ceil(yEnd + hEnd + FREE_BOT_PAD));
    }
    var priorH = parseFloat(board.style.minHeight) || 0;
    // Keep the browser from clamping scrollTop while a wheel gesture is active.
    freeStagePending = keepRunway && stageH < priorH;
    if (freeStagePending) return priorH;
    if (board.style.minHeight !== stageH + "px") {
      board.style.minHeight = stageH + "px";
    }
    return stageH;
  }
  function freeShiftScroll(col, delta, knownTop) {
    var top = typeof knownTop === "number" ? knownTop : col.scrollTop;
    var atJump = freeJump && !freeJump.moving && Math.abs(top - freeJump.top) <= 1;
    var next = Math.max(0, top + delta);
    col.scrollTop = next;
    freeLastShift += next - top;
    freeLastScrollTop = next;
    if (atJump) freeJump.top = next;
  }
  function freeReleasePin() {
    if (!freePin) return;
    freePin = null;
    clearTimeout(freePinTimer);
    if (freePinRaf) cancelAnimationFrame(freePinRaf);
    freePinTimer = freePinRaf = 0;
  }
  function freePinned() {
    var p = freePin;
    if (p && (Date.now() >= p.until || p.generation !== anchorUserScroll ||
        !p.node.isConnected || !p.col.contains(p.card) || !p.card.contains(p.node) || p.card.hidden || p.node.hidden ||
        p.card.hasAttribute("data-wb-offwindow") || p.card.hasAttribute("data-state-filtered") ||
        p.card.hasAttribute("data-max") || p.node.hasAttribute("data-max"))) freeReleasePin();
    return freePin;
  }
  function freeStartPinWindow(afterRail) {
    var p = freePinned();
    if (!p) return;
    if (p.recheck && !afterRail) {
      // The landing's scroll event queues the rail draw. Let that frame
      // publish the sticky offsets before rechecking the destination.
      freePinRaf = requestAnimationFrame(function () {
        freePinRaf = requestAnimationFrame(function () {
          freePinRaf = 0;
          freeStartPinWindow(true);
        });
      });
      return;
    }
    if (p.recheck) {
      if (!freeJump || Math.abs(p.col.scrollTop - freeJump.top) > 1) { freeReleasePin(); return; }
      var top = p.recheck();
      p.recheck = null;
      if (top !== undefined) {
        p.col.scrollTo({ top: top, behavior: "instant" });
        freeJump.top = p.col.scrollTop;
        p.offset = freeOffset(p.node, p.col);
      }
    }
    p.until = Date.now() + FREE_PIN_MS;
    freePinTimer = setTimeout(freeReleasePin, FREE_PIN_MS);
    if (p.dirty) freePinLayout();
  }
  function freeOffset(node, col) {
    return node.getBoundingClientRect().top - col.getBoundingClientRect().top - (col.clientTop || 0);
  }
  function freeLand(col, top, smooth, pin) {
    freeReleasePin();
    var from = col.scrollTop;
    var moving = !!(smooth && col.scrollTo && Math.abs(from - top) > 1);
    if (pin && pin.offset === undefined) pin.offset = freeOffset(pin.node, col);
    if (moving) col.scrollTo({ top: top, behavior: "smooth" });
    else {
      if (freeJump && freeJump.moving && col.scrollTo) col.scrollTo({ top: top, behavior: "instant" });
      else if (top !== from) col.scrollTop = top;
      top = col.scrollTop;
    }
    freeJump = { top: top, moving: moving };
    if (pin) {
      // Capture the projected landing during animation. Layout signals that
      // arrive in flight are applied when the native scroll settles.
      pin.col = col;
      pin.offset += from - top;
      pin.generation = anchorUserScroll;
      pin.until = Infinity;
      pin.dirty = false;
      freePin = pin;
      if (moving) freeScheduleIdle(col);
      else freeStartPinWindow();
    }
  }
  function freeLandTarget(node, top, smooth, offset, recheck) {
    var col = $("bcol"), card = freeActive && anchorOwner(node);
    if (!col || !card || !freePos.has(card) || !col.contains(node) ||
        card.hasAttribute("data-max") || node.hasAttribute("data-max")) return false;
    freeLand(col, top === undefined ? col.scrollTop : top, smooth && !reducedMotion(),
      { node: node, card: card, offset: offset, recheck: recheck });
    return true;
  }
  function freePinLayout() {
    var p = freePinned();
    if (!p) return;
    p.dirty = true;
    if (p.recheck || freePinRaf || (freeJump && freeJump.moving)) return;
    // Coalesce the height reports, observer callbacks and layout writes.
    freePinRaf = requestAnimationFrame(function () {
      freePinRaf = 0;
      if (!freePinned()) return;
      p.dirty = false;
      var top = p.col.scrollTop;
      // A focus or native scroll-into-view can move the scroller before its
      // scroll event arrives. That movement takes over just like input.
      if (!freeJump || Math.abs(top - freeJump.top) > 1) { freeReleasePin(); return; }
      var delta = freeOffset(p.node, p.col) - p.offset;
      if (Math.abs(delta) < FREE_RESIZE_EPS) return;
      p.col.scrollTop = top + delta;
      if (freeJump) freeJump.top = p.col.scrollTop;
    });
  }
  function freeLayout(opts) {
    opts = opts || {};
    freeLastShift = 0;
    var col = $("bcol"), board = region("board");
    var list = opts.list || anchorCards();
    if (!freeEnabled(col, list)) {
      if (freeActive || (col && col.hasAttribute && col.hasAttribute("data-board-free"))) freeSuspend();
      return false;
    }
    var wasActive = freeActive;
    var prevStart = freeWinStart, prevEnd = freeWinEnd;
    if (!col.hasAttribute || !col.hasAttribute("data-board-free")) col.setAttribute("data-board-free", "");
    freeActive = true;
    var scrollTick = !!opts.scrollTick;
    var viewH = anchorSized.h || col.clientHeight || window.innerHeight || 600;
    var aIdx = typeof opts.anchorIdx === "number" ? Math.max(0, Math.min(list.length - 1, opts.anchorIdx)) : 0;
    var aNode = list[aIdx];
    var aY = typeof opts.anchorY === "number" ? opts.anchorY : (freePos.has(aNode) ? freePos.get(aNode) : FREE_RUNWAY_MID);
    var scrubbing = typeof dialScrubbing !== "undefined" && !!dialScrubbing;
    var win = freeComputeWindow(list, aIdx, viewH);
    var start = win.start, end = win.end;

    // Phase A: mount & hydrate in-window cards.
    function mountSlot(idx) {
      var node = list[idx];
      if (!node) return;
      freeSetOffWindow(node, null);
      freeDeferredCollapse.delete(node);
      virtualExemptSet.delete(node);
      if (node.hasAttribute && node.hasAttribute("data-wb-virtual") && !scrubbing) {
        virtualHydrate(node, { immediate: idx === aIdx });
      }
    }
    for (var mi = start; mi <= end; mi++) mountSlot(mi);

    // On active scroll ticks use laid/cached/fallback heights instead of synchronous
    // offsetHeight reflows; freeCardRO reconciles real heights on the next frame.
    var measureFn = scrollTick ? freeLaidHeight : freeMeasureHeight;
    var heights = Object.create(null);
    for (var hi = start; hi <= end; hi++) heights[hi] = measureFn(list[hi]);
    var need = Math.max(1, viewH) * FREE_VIEWPORT_COVERAGE;
    var aboveH = 0;
    for (var ai = aIdx - 1; ai >= start; ai--) aboveH += heights[ai] + FREE_CARD_GAP;
    while (start > 0 && (aIdx - start) < FREE_MAX_WINDOW && aboveH < need) {
      start--;
      mountSlot(start);
      heights[start] = measureFn(list[start]);
      aboveH += heights[start] + FREE_CARD_GAP;
    }
    var belowH = 0;
    for (var bi = aIdx + 1; bi <= end; bi++) belowH += heights[bi] + FREE_CARD_GAP;
    while (end < list.length - 1 && (end - aIdx) < FREE_MAX_WINDOW && belowH < need) {
      end++;
      mountSlot(end);
      heights[end] = measureFn(list[end]);
      belowH += heights[end] + FREE_CARD_GAP;
    }

    // Mark off-window cards ("hidden" + body eviction vs "parked" + inert) in
    // one batched read-then-write pass. During active scroll, cards within
    // FREE_EVICT_MARGIN slots of the window defer collapse until scroll idle.
    var offNodes = [];
    var fullPass = !wasActive || !!opts.forceFull;
    if (fullPass) {
      freePos.forEach(function (_, fn) {
        if (!anchorCache || !anchorCache.indexByNode.has(fn)) {
          freePos.delete(fn);
          freeLaidH.delete(fn);
        }
      });
      freeLaidH.forEach(function (_, fn) {
        if (!freePos.has(fn)) freeLaidH.delete(fn);
      });
      for (var oi = 0; oi < list.length; oi++) {
        if (oi >= start && oi <= end) continue;
        offNodes.push(list[oi]);
      }
    } else {
      // O(1) delta: process cards leaving the previous window plus any dirty
      // far exempt topics or newly unpositioned cards.
      for (var pi = prevStart; pi <= prevEnd; pi++) {
        if (pi < 0 || pi >= list.length || (pi >= start && pi <= end)) continue;
        offNodes.push(list[pi]);
      }
      if (virtualExemptDirty.size) {
        Array.from(virtualExemptDirty).forEach(function (dn) {
          virtualExemptDirty.delete(dn);
          var di = anchorCache && anchorCache.indexByNode.has(dn) ? anchorCache.indexByNode.get(dn) : -1;
          if (di >= 0 && (di < start || di > end)) offNodes.push(dn);
        });
      }
    }
    var hysteresisCheck = scrollTick && !scrubbing ? function (n) {
      var ni = anchorCache && anchorCache.indexByNode.has(n) ? anchorCache.indexByNode.get(n) : -1;
      return ni >= 0 && ni >= start - FREE_EVICT_MARGIN && ni <= end + FREE_EVICT_MARGIN;
    } : null;
    if (!scrubbing && freeDeferredCollapse.size) {
      Array.from(freeDeferredCollapse).forEach(function (dn) {
        if (!dn || !dn.isConnected) { freeDeferredCollapse.delete(dn); return; }
        var di = anchorCache && anchorCache.indexByNode.has(dn) ? anchorCache.indexByNode.get(dn) : -1;
        if (di < 0 || (di >= start && di <= end)) { freeDeferredCollapse.delete(dn); return; }
        if (hysteresisCheck && hysteresisCheck(dn)) return;
        freeDeferredCollapse.delete(dn);
        offNodes.push(dn);
      });
    }
    if (offNodes.length) {
      freeApplyOffWindow(offNodes, scrubbing, { cold: !wasActive, deferCheck: hysteresisCheck });
    }

    // Keep WsBoardVirtual's near set aligned with the active free-canvas window.
    var nextNear = new Set();
    for (var ni = start; ni <= end; ni++) nextNear.add(list[ni]);
    virtualNearSet = nextNear;
    virtualInitialized = true;
    virtualLastBounded = true;

    // Phase B: radiate Designated-Y downward from aIdx and upward from aIdx.
    var ys = Object.create(null);
    ys[aIdx] = aY;
    for (var di2 = aIdx + 1; di2 <= end; di2++) {
      ys[di2] = ys[di2 - 1] + heights[di2 - 1] + FREE_CARD_GAP;
    }
    for (var ui = aIdx - 1; ui >= start; ui--) {
      ys[ui] = ys[ui + 1] - FREE_CARD_GAP - heights[ui];
    }

    // Phase C: top boundary clamp (y_0 = topPad) & bottom clamp / runway minHeight.
    if (start === 0) {
      var topPad = freeTopPad(board);
      var shift = topPad - ys[0];
      if (Math.abs(shift) >= FREE_RESIZE_EPS) {
        var deferTopRebase = false;
        if (scrollTick && shift < 0) {
          var curTop = typeof opts.scrollTop === "number" ? opts.scrollTop : col.scrollTop;
          if (curTop > ys[0]) deferTopRebase = true;
        }
        if (deferTopRebase) {
          freeTopRebasePending = true;
          if (!freeIdleTimer) freeScheduleIdle();
        } else {
          for (var si = start; si <= end; si++) ys[si] += shift;
          if ((!opts.jump || opts.smooth) && wasActive && !freePinned()) freeShiftScroll(col, shift, opts.scrollTop);
          freeTopRebasePending = false;
        }
      } else {
        freeTopRebasePending = false;
      }
    } else {
      freeTopRebasePending = false;
    }
    var stageH = freeUpdateMoreAndStage(board, list, end, ys[end], heights[end], !opts.jump && !!freeIdleTimer);

    // Write style.top only when changed; never mutate width/height.
    for (var wi = start; wi <= end; wi++) {
      var card = list[wi];
      freePos.set(card, ys[wi]);
      freeLaidH.set(card, heights[wi]);
      if (!card.hasAttribute || !card.hasAttribute("data-max")) {
        var topStr = Math.round(ys[wi]) + "px";
        if (card.style && card.style.top !== topStr) card.style.top = topStr;
      }
    }
    freeWinStart = start;
    freeWinEnd = end;
    freeAnchorIdx = aIdx;

    // Phase D: single shared ResizeObserver strictly on mounted window.
    freeSyncRO(list, start, end);

    // Phase E: synchronous jump positioning (center short/medium, top-align tall).
    if (opts.jump) {
      // Match the rail's stick line when a tall card lands at the top.
      var topClear = anchorRailInset(col) + anchorCardCap(list[aIdx], list);
      var botClear = anchorBotReserve(Math.min(2, Math.max(0, list.length - 1 - aIdx)));
      var usableH = Math.max(120, viewH - topClear - botClear);
      var targetH = heights[aIdx];
      var targetScroll;
      if (typeof opts.jumpOffset === "number" && isFinite(opts.jumpOffset)) {
        targetScroll = ys[aIdx] - opts.jumpOffset;
      } else if (opts.centerJump !== false && targetH < usableH) {
        targetScroll = ys[aIdx] - topClear - (usableH - targetH) / 2;
      } else {
        targetScroll = ys[aIdx] - topClear;
      }
      var maxScroll = Math.max(0, stageH - viewH);
      targetScroll = Math.max(0, Math.min(maxScroll, Math.round(targetScroll)));
      // A centered or clamped jump can leave a preceding card visible. Keep
      // the requested selection at the landing offset until the board moves.
      var pin = opts.pin !== false && !scrubbing ? { node: aNode, card: aNode } : null;
      freeLand(col, targetScroll, opts.smooth && !reducedMotion(), pin);
    } else freePinLayout();
    return true;
  }
  function freeOnCardResize(entries) {
    if (!freeActive || freeSuspended || !entries || !entries.length) return;
    var col = $("bcol"), board = region("board"), list = anchorCards();
    if (!col || !board || !list.length || freeWinEnd < freeWinStart) return;
    var viewTopY = col.scrollTop + anchorSized.padTop + anchorRailInset(col);
    var anchor = freeFindTopVisibleIdx(list, col, viewTopY);
    var mutated = false;
    for (var i = 0; i < entries.length; i++) {
      var e = entries[i], card = e && e.target;
      if (!card || !anchorCache || !anchorCache.indexByNode.has(card)) continue;
      if (card.hasAttribute && (card.hasAttribute("data-wb-offwindow") || card.hasAttribute("data-max"))) continue;
      var k = anchorCache.indexByNode.get(card);
      if (k < freeWinStart || k > freeWinEnd) continue;
      var bb = e.borderBoxSize && e.borderBoxSize[0];
      var newH = (bb ? bb.blockSize : 0) || card.offsetHeight || (e.contentRect ? e.contentRect.height : 0);
      if (!(newH > 0)) continue;
      var key = anchorId(card);
      var oldH = freeLaidH.has(card)
        ? freeLaidH.get(card)
        : (key !== null && freeHeightCache.has(key) ? freeHeightCache.get(key) : FREE_FALLBACK_H);
      var dh = newH - oldH;
      if (Math.abs(dh) < FREE_RESIZE_EPS) {
        if (key !== null && !freeHeightCache.has(key)) freeHeightCache.set(key, newH);
        continue;
      }
      freeLaidH.set(card, newH);
      if (key !== null) freeHeightCache.set(key, newH);
      mutated = true;
      if (k < anchor) {
        // Above-anchor resize grows upward from y_anchor: 0px shift on anchor & visible cards.
        for (var u = freeWinStart; u <= k; u++) {
          var nu = list[u], yu = freePos.get(nu);
          if (yu !== undefined) freePos.set(nu, yu - dh);
        }
      } else {
        // Anchor or below-anchor resize pushes cards below k downward.
        for (var d = k + 1; d <= freeWinEnd; d++) {
          var nd = list[d], yd = freePos.get(nd);
          if (yd !== undefined) freePos.set(nd, yd + dh);
        }
      }
    }
    if (!mutated) return;
    if (freeWinStart === 0) {
      var y0 = freePos.get(list[0]);
      if (y0 !== undefined) {
        var shift = freeTopPad(board) - y0;
        if (Math.abs(shift) >= FREE_RESIZE_EPS) {
          if (shift < 0 && freeIdleTimer) {
            freeTopRebasePending = true;
          } else {
            for (var si = freeWinStart; si <= freeWinEnd; si++) {
              var ns = list[si], ys = freePos.get(ns);
              if (ys !== undefined) freePos.set(ns, ys + shift);
            }
            if (!freePinned()) freeShiftScroll(col, shift);
            freeTopRebasePending = false;
          }
        } else {
          freeTopRebasePending = false;
        }
      }
    }
    for (var wi = freeWinStart; wi <= freeWinEnd; wi++) {
      var wc = list[wi], wy = freePos.get(wc);
      if (wy === undefined || (wc.hasAttribute && wc.hasAttribute("data-max"))) continue;
      var topStr = Math.round(wy) + "px";
      if (wc.style && wc.style.top !== topStr) wc.style.top = topStr;
    }
    if (freeWinEnd === list.length - 1) {
      var lastCard = list[freeWinEnd], yEnd = freePos.get(lastCard);
      if (yEnd !== undefined) {
        // #r-board is shallower than its observed topic cards and is itself
        // observed by cards.js; defer stage minHeight writes out of RO delivery.
        if (typeof requestAnimationFrame !== "function") {
          freeUpdateMoreAndStage(board, list, freeWinEnd, yEnd, freeLaidHeight(lastCard), !!freeIdleTimer);
        } else if (!freeStageRaf) {
          freeStageRaf = requestAnimationFrame(function () {
            freeStageRaf = 0;
            if (!freeActive || freeSuspended) return;
            var curBoard = region("board"), curList = anchorCards();
            if (!curBoard || !curList.length || freeWinEnd !== curList.length - 1) return;
            var curLast = curList[freeWinEnd], curY = freePos.get(curLast);
            if (curY !== undefined) freeUpdateMoreAndStage(curBoard, curList, freeWinEnd, curY, freeLaidHeight(curLast), !!freeIdleTimer);
          });
        }
      }
    }
    freePinLayout();
  }
  function freeFlushScrolledOutHeld() {
    if (typeof boardPending === "undefined" || !Object.keys(boardPending).length) return;
    if (typeof boardGuards !== "function" || typeof boardFlushViewed !== "function") return;
    var guarded = boardGuards();
    if (Object.keys(boardPending).some(function (k) { return guarded.indexOf(k) < 0; })) {
      boardFlushViewed(false);
    }
  }
  function freeFinishJump(col, knownTop) {
    if (!freeJump || !freeJump.moving) return;
    freeJump.top = typeof knownTop === "number" ? knownTop : col.scrollTop;
    freeJump.moving = false;
    freeStartPinWindow();
  }
  function freeScrollTick() {
    freeScrollRaf = 0;
    if (!freeActive || freeSuspended) return;
    var col = $("bcol"), list = anchorCards();
    if (!col || !list.length || freeWinEnd < freeWinStart) return;
    var top = col.scrollTop;
    freeLastScrollTop = top;
    if (freePendingScrollEnd) {
      freePendingScrollEnd = false;
      // An interrupted native wheel transaction can emit its own scrollend.
      if (freeJump && Math.abs(top - freeJump.top) <= 1) freeFinishJump(col, top);
    }
    if (freeJump && !freeJump.moving && Math.abs(top - freeJump.top) > 1) {
      freeReleasePin();
      freeJump = null;
    }
    var inset = anchorRailInset(col);
    var viewTopY = top + anchorSized.padTop + inset;
    var nextAnchor = freeJump && !freeJump.moving ? freeAnchorIdx : freeFindTopVisibleIdx(list, col, viewTopY);
    var nextNode = list[nextAnchor];
    if (!freeJump && nextNode && nextNode !== anchorLast) anchorMark(nextNode, { skipVirtual: true });
    var viewH = anchorSized.h || col.clientHeight || window.innerHeight || 600;
    var yFirst = freePos.get(list[freeWinStart]);
    var lastCard = list[freeWinEnd];
    var yLastBottom = (freePos.get(lastCard) || 0) + freeLaidHeight(lastCard);
    var needSlide = nextAnchor !== freeAnchorIdx ||
      (freeWinStart > 0 && yFirst !== undefined && (top - yFirst) < viewH * 0.75) ||
      (freeWinEnd < list.length - 1 && (yLastBottom - (top + viewH)) < viewH * 0.75) ||
      (freeTopRebasePending && freeWinStart === 0 && yFirst !== undefined && top <= yFirst);
    var didLayout = false;
    if (!freeJump && (needSlide || virtualExemptDirty.size)) {
      didLayout = freeLayout({ anchorIdx: nextAnchor, anchorY: freePos.get(nextNode), list: list, scrollTick: true, scrollTop: top });
    }
    if (typeof boardDialScroll === "function" && nextNode) {
      var yCur = freePos.get(nextNode);
      if (yCur !== undefined) {
        var curTop = top + (didLayout ? freeLastShift : 0);
        var curTopY = curTop + anchorSized.padTop + inset;
        var span = Math.max(1, freeLaidHeight(nextNode) + FREE_CARD_GAP);
        var frac = Math.max(0, Math.min(0.999, (curTopY - yCur) / span));
        boardDialScroll(Math.max(0, Math.min(list.length - 1, nextAnchor + frac)));
      }
    }
    freeFlushScrolledOutHeld();
  }
  function freeIdleRecenter() {
    freeIdleTimer = 0;
    try {
      if (!freeActive || freeSuspended) return;
      if (typeof dialScrubbing !== "undefined" && dialScrubbing) return;
      var col = $("bcol"), board = region("board"), list = anchorCards();
      if (!col || !board || !list.length || freeWinEnd < freeWinStart) return;
      if (freeJump && freeJump.moving) {
        freeFinishJump(col);
        freeScrollTick();
      } else {
        freeFlushScrolledOutHeld();
      }
      // Flush cards whose collapse was deferred by hysteresis during active scroll.
      if (freeDeferredCollapse.size) {
        var toFlush = [];
        Array.from(freeDeferredCollapse).forEach(function (dn) {
          freeDeferredCollapse.delete(dn);
          if (!dn || !dn.isConnected) return;
          var di = anchorCache && anchorCache.indexByNode.has(dn) ? anchorCache.indexByNode.get(dn) : -1;
          if (di >= 0 && (di < freeWinStart || di > freeWinEnd)) toFlush.push(dn);
        });
        if (toFlush.length) freeApplyOffWindow(toFlush, false, { cold: false });
      }
      // Complete any deferred top-boundary rebase once scrolling stops, shifting
      // scrollTop before shrinking board minHeight so the browser never clamps.
      if (freeWinStart === 0) {
        var y0 = freePos.get(list[0]);
        if (y0 !== undefined) {
          var topShift = freeTopPad(board) - y0;
          if (Math.abs(topShift) >= FREE_RESIZE_EPS) {
            for (var ti = freeWinStart; ti <= freeWinEnd; ti++) {
              var tc = list[ti], ty = freePos.get(tc);
              if (ty === undefined) continue;
              var nty = ty + topShift;
              freePos.set(tc, nty);
              if (!tc.hasAttribute || !tc.hasAttribute("data-max")) {
                var tTopStr = Math.round(nty) + "px";
                if (tc.style && tc.style.top !== tTopStr) tc.style.top = tTopStr;
              }
            }
            if (!freePinned()) freeShiftScroll(col, topShift);
            var endNode = list[freeWinEnd], endY = freePos.get(endNode);
            if (endY !== undefined) {
              freeUpdateMoreAndStage(board, list, freeWinEnd, endY, freeLaidHeight(endNode), false);
            }
            freePinLayout();
          }
        }
        freeTopRebasePending = false;
      }
      if (freeStagePending) {
        var tail = list[freeWinEnd], tailY = freePos.get(tail);
        if (tailY !== undefined) freeUpdateMoreAndStage(board, list, freeWinEnd, tailY, freeLaidHeight(tail), false);
        freePinLayout();
      }
      if (freeWinStart === 0 || freeWinEnd === list.length - 1) return;
      var yFirst = freePos.get(list[freeWinStart]);
      var lastNode = list[freeWinEnd];
      var yLast = freePos.get(lastNode);
      if (yFirst === undefined || yLast === undefined) return;
      var yLastBottom = yLast + freeLaidHeight(lastNode);
      var stageH = parseFloat(board.style.minHeight) || FREE_RUNWAY_HEIGHT;
      if (yFirst >= FREE_RUNWAY_MARGIN && yLastBottom <= stageH - FREE_RUNWAY_MARGIN) return;
      var anchorNodeCur = list[freeAnchorIdx];
      var yAnchor = freePos.get(anchorNodeCur);
      if (yAnchor === undefined) return;
      var delta = FREE_RUNWAY_MID - yAnchor;
      if (Math.abs(delta) < 1) return;
      for (var i = freeWinStart; i <= freeWinEnd; i++) {
        var c = list[i], cy = freePos.get(c);
        if (cy === undefined) continue;
        var ny = cy + delta;
        freePos.set(c, ny);
        if (!c.hasAttribute || !c.hasAttribute("data-max")) {
          var topStr = Math.round(ny) + "px";
          if (c.style && c.style.top !== topStr) c.style.top = topStr;
        }
      }
      freeShiftScroll(col, delta);
    } finally {
      if (typeof freeNotifyScrollIdle === "function") freeNotifyScrollIdle();
    }
  }
  function freeScheduleIdle() {
    if (freeIdleTimer) clearTimeout(freeIdleTimer);
    var gen = ++freeScrollGen, generation = anchorUserScroll;
    freeIdleTimer = setTimeout(function () {
      freeIdleTimer = 0;
      if (gen === freeScrollGen && generation === anchorUserScroll) freeIdleRecenter();
    }, FREE_IDLE_RECENTER_MS);
  }
  function freeOnScroll() {
    if (!freeActive || freeSuspended) return;
    freeScheduleIdle();
    if (!freeScrollRaf && typeof requestAnimationFrame === "function") {
      freeScrollRaf = requestAnimationFrame(freeScrollTick);
    }
  }
  function freeWindowInfo() {
    var list = anchorCards();
    return {
      active: !!freeActive,
      start: freeWinStart,
      end: freeWinEnd,
      anchorIdx: freeAnchorIdx,
      list: list,
      railBefore: anchorCache ? anchorCache.railBefore : [],
      railList: anchorCache ? anchorCache.railList : [],
      railTotal: anchorCache ? anchorCache.railCount : 0,
      indexOf: function (n) {
        if (!n || !anchorCache) return -1;
        var idx = anchorCache.indexByNode.get(n);
        return idx === undefined ? -1 : idx;
      },
      posOf: function (n) { return freePos.has(n) ? freePos.get(n) : undefined; },
      heightOf: function (n) { return freeLaidHeight(n); }
    };
  }
  function anchorVisibleCards() {
    if ((doc && ((typeof docHidden === "function" ? docHidden() : doc.hidden) || (doc.documentElement && doc.documentElement.hasAttribute && doc.documentElement.hasAttribute("data-animation-paused")))) || (typeof shHidden !== "undefined" && !!shHidden)) {
      return [];
    }
    if (!freeActive) {
      var cur = anchorCurrent();
      return cur ? [cur] : [];
    }
    var col = $("bcol"), list = anchorCards();
    if (!col || !list.length || freeWinEnd < freeWinStart) return [];
    var viewH = anchorSized.h || col.clientHeight || window.innerHeight || 600;
    var topY = col.scrollTop + anchorSized.padTop + anchorRailInset(col);
    var botY = col.scrollTop + viewH - anchorBotReserve(0);
    var out = [];
    for (var i = Math.max(0, freeWinStart); i <= Math.min(list.length - 1, freeWinEnd); i++) {
      var c = list[i], y = freePos.get(c);
      if (y === undefined) continue;
      var h = freeLaidHeight(c);
      if (y + h > topY + 0.5 && y < botY - 0.5) out.push(c);
    }
    if (!out.length) {
      var curCard = anchorCurrent();
      if (curCard) out.push(curCard);
    }
    return out;
  }

  function anchorAlign(col, n, smooth, bounded) {
    if (!col || !n || !n.isConnected) return false;
    var b = bounded === undefined ? anchorIsBounded(col) : bounded;
    var sc = anchorScroller(col, b), delta, target;
    if (!b && window.wbHistory && window.wbHistory.keeping && window.wbHistory.keeping(sc)) return false;
    if (n.getAttribute && n.getAttribute("data-anchor-cap") === "0") {
      target = 0;
      delta = -sc.scrollTop;
    } else {
      var c = b ? col.getBoundingClientRect() : null, r = n.getBoundingClientRect();
      delta = r.top - anchorLandingTop(col, c, n);
      target = Math.max(0, sc.scrollTop + delta);
    }
    if (anchorProg && anchorProg.key === anchorId(n)) anchorProg.target = target;
    if (Math.abs(delta) <= 0.5) return false;
    if (sc.scrollTo) sc.scrollTo({ top: target, behavior: smooth ? "smooth" : "auto" });
    else sc.scrollTop = target;
    if (anchorProg && !smooth) anchorProg.top = sc.scrollTop;
    return true;
  }
  function anchorWaitFrames(pid, count) {
    if (typeof requestAnimationFrame !== "function") return;
    requestAnimationFrame(function () {
      if (!anchorProg || anchorProg.id !== pid) return;
      if (count > 1) { anchorWaitFrames(pid, count - 1); return; }
      anchorRealign(true);
    });
  }
  // select makes node n the current card, synchronously un-hiding/hydrating and
  // positioning it in free-canvas mode (centered for short/medium cards,
  // top-aligned for tall cards) so 2-level jump callers can measure immediately.
  function anchorSelect(n, opt) {
    if (!n || !n.isConnected) return false;
    var col = $("bcol");
    if (!col || !col.contains(n)) return false;
    var key = anchorId(n);
    if (key === null) return false;
    var left = anchorKey !== null && anchorKey !== key;
    if (freeEnabled(col, anchorCards())) {
      var wasMounted = freeActive && freePos.has(n) && (!n.hasAttribute || !n.hasAttribute("data-wb-offwindow"));
      var prevMountedY = wasMounted ? freePos.get(n) : undefined;
      freeSetOffWindow(n, null);
      var isScrub = typeof dialScrubbing !== "undefined" && !!dialScrubbing;
      if (!isScrub && typeof virtualHydrate === "function" && n.hasAttribute && n.hasAttribute("data-wb-virtual")) {
        virtualHydrate(n, { immediate: true });
      }
      if (anchorKey !== key || anchorLast !== n) anchorMark(n, { skipVirtual: true });
      if (left && !(opt && opt.keep) && Object.keys(boardPending).length) {
        boardFlushViewed(true);
        n = anchorNode(key) || n;
      }
      var list = anchorCards();
      var idx = anchorCache && anchorCache.indexByNode.has(n) ? anchorCache.indexByNode.get(n) : list.indexOf(n);
      if (idx < 0) return false;
      var still = !!(opt && opt.still);
      if (!still) freeReleasePin();
      var anchorY = wasMounted ? (freePos.has(n) ? freePos.get(n) : prevMountedY) : FREE_RUNWAY_MID;
      var wantSmooth = !still && wasMounted && !!(opt && opt.smooth);
      freeLayout({
        anchorIdx: idx,
        anchorY: anchorY,
        jump: !still,
        centerJump: !(opt && opt.alignTop),
        pin: !(opt && opt.pin === false),
        jumpOffset: opt && opt.jumpOffset,
        smooth: wantSmooth,
        forceFull: !!(opt && opt.fullVirtual) || !freeActive,
        list: list
      });
      return true;
    }
    if (typeof virtualHydrate === "function" && (typeof dialScrubbing === "undefined" || !dialScrubbing) && n.hasAttribute && n.hasAttribute("data-wb-virtual")) {
      virtualHydrate(n, { immediate: true });
    }
    if (anchorKey !== key || anchorLast !== n) anchorMark(n, opt && opt.fullVirtual ? { full: true } : undefined);
    if (left && !(opt && opt.keep) && Object.keys(boardPending).length) {
      boardFlushViewed(true);
      n = anchorNode(key) || n;
    }
    if (!(opt && opt.still)) {
      var calm = reducedMotion(), wantSmooth = !!(opt && opt.smooth && !calm);
      var bar = window.WsCards && window.WsCards.toolbar && window.WsCards.toolbar();
      var prevFloat = bar ? bar.hasAttribute("data-floating") : null;
      var pid = ++anchorProgSeq;
      var bounded = anchorIsBounded(col), sc = anchorScroller(col, bounded);
      anchorProg = { key: key, id: pid, scroller: sc, smooth: wantSmooth, until: Date.now() + (wantSmooth ? 1500 : 400), tries: 0, waitFloat: false, scrollDone: false, target: sc.scrollTop, top: sc.scrollTop };
      var moved = anchorAlign(col, n, wantSmooth, bounded);
      if (!moved) { anchorProg = null; return true; }
      if (prevFloat !== null) anchorProg.waitFloat = (anchorProg.target > anchorSized.padTop + 1) !== prevFloat;
      if (!wantSmooth) anchorWaitFrames(pid, anchorProg.waitFloat ? 5 : 2);
    }
    return true;
  }
  // step selects the card after (d = 1) or before (d = -1) the current one.
  function anchorStep(d) {
    var list = anchorCards(), cur = anchorCurrent();
    var at = cur && anchorCache && anchorCache.indexByNode.has(cur) ? anchorCache.indexByNode.get(cur) : list.indexOf(cur);
    if (at < 0) { anchorSelect(list[d > 0 ? 0 : list.length - 1], { smooth: true }); return true; }
    var to = list[at + d];
    if (to) { anchorSelect(to, { smooth: true }); return true; }
    // Past the last card the board's tail (Show more closed topics, the
    // runway) is reachable; past the first, nothing.
    if (d > 0) {
      var col = $("bcol");
      if (col) {
        freeReleasePin();
        var height = col.scrollHeight, smooth = !reducedMotion();
        if (freeActive && smooth) {
          var target = Math.max(0, height - col.clientHeight);
          if (Math.abs(col.scrollTop - target) > 1) freeJump = { top: target, moving: true };
        }
        col.scrollTo({ top: height, behavior: smooth ? "smooth" : "auto" });
      }
    }
    return false;
  }
  // fallback is the card to take when the current one leaves the order: the
  // next card, else the previous one (the list before the change).
  function anchorFallback(before, gone) {
    var at = before.indexOf(gone), list = anchorCards();
    for (var i = at + 1; i < before.length; i++) if (list.indexOf(before[i]) >= 0) return before[i];
    for (var j = at - 1; j >= 0; j--) if (list.indexOf(before[j]) >= 0) return before[j];
    return list[0] || null;
  }
  // settle keeps selection and Designated-Y coordinates consistent after any
  // board mutation (morphBoard, loadClosed, dock/undock, maximize/unmaximize).
  function anchorSlotCaps(list) {
    var cards = list || anchorCards(), next0 = null, nextR0 = null, next1 = null, seen = 0;
    for (var i = 0; i < cards.length; i++) {
      var c = cards[i], rail = c.hasAttribute("data-topic") || c.hasAttribute("data-rail");
      if (i === 0) next0 = c;
      else if (rail && seen === 0) nextR0 = c;
      else if (rail && seen === 1) { next1 = c; break; }
      if (rail) seen++;
    }
    var prev = [anchorCap0, anchorCapR0, anchorCap1];
    for (var j = 0; j < prev.length; j++) {
      var p = prev[j];
      if (p && p !== next0 && p !== nextR0 && p !== next1) p.removeAttribute("data-anchor-cap");
    }
    if (next0 && (!next0.getAttribute || next0.getAttribute("data-anchor-cap") !== "0")) next0.setAttribute("data-anchor-cap", "0");
    if (nextR0 && (!nextR0.getAttribute || nextR0.getAttribute("data-anchor-cap") !== "rail0")) nextR0.setAttribute("data-anchor-cap", "rail0");
    if (next1 && (!next1.getAttribute || next1.getAttribute("data-anchor-cap") !== "1")) next1.setAttribute("data-anchor-cap", "1");
    anchorCap0 = next0;
    anchorCapR0 = nextR0;
    anchorCap1 = next1;
    var nextBots = [], botMap = new Map(), afterRail = 0;
    for (var k = cards.length - 1; k >= 0; k--) {
      var ck = cards[k];
      if (afterRail < 2) {
        var val = String(afterRail);
        nextBots.push(ck);
        botMap.set(ck, val);
      }
      if (ck.hasAttribute("data-topic") || ck.hasAttribute("data-rail")) {
        afterRail++;
        if (afterRail >= 2) break;
      }
    }
    for (var m = 0; m < anchorBotNodes.length; m++) {
      var pb = anchorBotNodes[m];
      if (pb && !botMap.has(pb)) pb.removeAttribute("data-anchor-bot");
    }
    botMap.forEach(function (val, node) {
      if (!node.getAttribute || node.getAttribute("data-anchor-bot") !== val) node.setAttribute("data-anchor-bot", val);
    });
    anchorBotNodes = nextBots;
    var lastNode = cards.length ? cards[cards.length - 1] : null;
    if (anchorLastNode && anchorLastNode !== lastNode) anchorLastNode.removeAttribute("data-anchor-last");
    if (lastNode && (!lastNode.getAttribute || lastNode.getAttribute("data-anchor-last") !== "1")) lastNode.setAttribute("data-anchor-last", "1");
    anchorLastNode = lastNode;
  }
  function anchorSettle(before) {
    if (freeSuspended && !(doc.documentElement && doc.documentElement.hasAttribute("data-overview"))) {
      freeSuspended = false;
    }
    var list = anchorCards(true), n = anchorNode(anchorKey), col = $("bcol");
    anchorSlotCaps(list);
    if (!list.length) {
      if (freeActive || (col && col.hasAttribute && col.hasAttribute("data-board-free"))) freeSuspend();
      anchorMark(null, { full: true });
      return;
    }
    if (n) {
      if (freeEnabled(col, list)) {
        var idx = anchorCache && anchorCache.indexByNode.has(n) ? anchorCache.indexByNode.get(n) : list.indexOf(n);
        var prevY = freePos.has(n) ? freePos.get(n)
          : (anchorLast && anchorId(anchorLast) === anchorKey && freePos.has(anchorLast) ? freePos.get(anchorLast) : null);
        if (n !== anchorLast) anchorMark(n, { skipVirtual: true });
        var curY = prevY !== null ? prevY : (idx === 0 ? freeTopPad(region("board")) : FREE_RUNWAY_MID);
        freeLayout({ anchorIdx: Math.max(0, idx), anchorY: curY, jump: false, forceFull: true, list: list });
        return;
      }
      if (n !== anchorLast) anchorMark(n, { full: true });
      else if (typeof virtualReconcile === "function" && (typeof dialScrubbing === "undefined" || !dialScrubbing)) virtualReconcile({ full: true });
      return;
    }
    var saved = null;
    if (anchorKey === null) try { saved = window.sessionStorage.getItem(ANCHOR_STORE); } catch (e) {}
    var key = saved !== null ? saved : null, next = key !== null ? anchorNode(key) : null;
    if (!next && before && anchorLast) next = anchorFallback(before, anchorLast);
    // Restoring a saved jump may pin it. A missing card's automatic fallback
    // leaves the former target's pin released.
    anchorSelect(next || list[0], { keep: true, still: anchorKey === null && !next, pin: key !== null && !!next, fullVirtual: true, jumpOffset: next && typeof boardOffsetTake === "function" ? boardOffsetTake(key) : null });
  }
  // jump resolves a board topic or card to its containing card, then lets the
  // caller scroll inside it.
  function anchorOwner(n) {
    if (typeof anchorDrainMutations === "function") anchorDrainMutations();
    if (!anchorCache) anchorCards(true);
    for (var p = n; p && p.nodeType === 1 && p !== doc.body; p = p.parentNode) {
      if (anchorId(p) !== null && anchorCache.indexByNode.has(p)) return p;
    }
    return null;
  }
  // Exposure of a node for staging: in free-canvas mode, any card intersecting
  // #bcol's visible viewport is exposed (not only anchorCurrent()). Outside
  // free-canvas mode, retains the current-card viewport clip.
  function anchorView() {
    var snap = boardSnap;
    if (snap && snap.anchorView !== undefined) return snap.anchorView;
    var col = $("bcol"), v = null;
    if (col) {
      var c = col.getBoundingClientRect();
      if (freeActive) {
        v = {
          card: null,
          free: true,
          top: c.top + (col.clientTop || 0) + anchorSized.padTop + anchorRailInset(col),
          bottom: Math.min(c.bottom - anchorBotReserve(0), window.innerHeight || Infinity),
          left: c.left,
          right: c.right
        };
      } else {
        var cur = anchorCurrent();
        if (cur) {
          var r = cur.getBoundingClientRect();
          v = { card: cur, top: Math.max(anchorLandingTop(col, c, cur), r.top), bottom: Math.min(c.bottom - anchorBotReserve(), r.bottom, window.innerHeight || Infinity),
            left: Math.max(c.left, r.left), right: Math.min(c.right, r.right) };
        }
      }
    }
    if (snap) snap.anchorView = v;
    return v;
  }
  function anchorExposed(n) {
    var v = anchorView(), w = window.wbHistory;
    if (!v) return false;
    if (v.free) {
      var card = anchorOwner(n);
      if (!card || (card.hasAttribute && card.hasAttribute("data-wb-offwindow"))) return false;
    } else if (!v.card || !(n === v.card || v.card.contains(n))) {
      return false;
    }
    if (w && w.rendered && !(boardSnap && boardSnap.rendered ? boardSnap.rendered(n) : w.rendered(n))) return false;
    var r = n.getBoundingClientRect();
    return r.height > 0 && Math.min(v.bottom, r.bottom) - Math.max(v.top, r.top) >= 1 && Math.min(v.right, r.right) - Math.max(v.left, r.left) >= 1;
  }
  // --- sizes: the usable scrollport ---------------------------------------------
  function anchorSetVar(s, k, v) { if (!s.getPropertyValue || s.getPropertyValue(k) !== v) s.setProperty(k, v); }
  function anchorApplySizes(col) {
    var inset = anchorRailInset(col), topSpace = inset + anchorCapH(2);
    var bot2 = anchorBotReserve(2);
    anchorSized.inset = inset;
    var s = col.style;
    anchorSetVar(s, "--board-toolbar", topSpace + "px");
    anchorSetVar(s, "--board-pad-top", anchorSized.padTop + "px");
    if (!anchorSized.bounded) {
      if (col.removeAttribute) col.removeAttribute("data-board-bounded");
      if (s.removeProperty) {
        s.removeProperty("--board-h");
        s.removeProperty("--board-usable");
      }
      if (freeActive || (col.hasAttribute && col.hasAttribute("data-board-free"))) freeSuspend();
      return;
    }
    if (col.setAttribute && (!col.hasAttribute || !col.hasAttribute("data-board-bounded"))) col.setAttribute("data-board-bounded", "");
    anchorSetVar(s, "--board-h", anchorSized.h + "px");
    var usable = Math.max(120, anchorSized.h - topSpace - bot2);
    anchorSetVar(s, "--board-usable", usable + "px");
  }
  function anchorObserveAux(col) {
    if (!anchorRO || !col) return;
    var face = anchorToolbarFace(), reply = col.querySelector(".reply");
    if (face !== anchorFaceEl) {
      if (anchorFaceEl) anchorRO.unobserve(anchorFaceEl);
      anchorFaceEl = face;
      if (face) anchorRO.observe(face);
    }
    if (reply !== anchorReplyEl) {
      if (anchorReplyEl) anchorRO.unobserve(anchorReplyEl);
      anchorReplyEl = reply;
      anchorSized.reply = 0;
      if (reply) anchorRO.observe(reply);
    }
  }
  function anchorRealign(fromInsetOrFrame) {
    var p = anchorProg;
    if (!p || p.key !== anchorKey || Date.now() > p.until) { anchorProg = null; return; }
    var col = $("bcol"), n = anchorNode(p.key);
    if (!col || !n || !n.isConnected) return;
    if (!p.smooth && Math.abs(p.scroller.scrollTop - p.top) > 1) { anchorProg = null; return; }
    var moved = anchorAlign(col, n, !p.scrollDone && p.smooth);
    if (moved) p.scrollDone = false;
    if (fromInsetOrFrame) {
      p.waitFloat = false;
      if (p.scrollDone && !moved) anchorProg = null;
    }
  }
  function anchorSize(entries) {
    var col = $("bcol");
    if (!col) return;
    anchorObserveAux(col);
    var face = anchorFaceEl, reply = anchorReplyEl, insetChanged = false, prevBounded = anchorSized.bounded, prevH = anchorSized.h;
    entries.forEach(function (e) {
      var box = e.contentBoxSize && e.contentBoxSize[0], bb = e.borderBoxSize && e.borderBoxSize[0];
      if (e.target === col) {
        var bw = box ? box.inlineSize : e.contentRect.width, bh = box ? box.blockSize : e.contentRect.height;
        // A hidden frame reports a 0x0 box: keep the last valid sizes so it never
        // marks the board settled before a real box exists (boardRevealNow re-reads on show).
        if (!(bw > 0 && bh > 0)) return;
        var cs = getComputedStyle(col);
        anchorSized.bounded = /auto|scroll/.test(cs.overflowY);
        anchorSized.w = bw;
        anchorSized.h = anchorSized.bounded ? bh : 0;
        anchorSized.padTop = parseFloat(cs.paddingTop) || 0;
      } else if (e.target === face) {
        var nextBar = bb ? bb.blockSize : e.contentRect.height;
        if (nextBar !== anchorSized.bar) insetChanged = true;
        anchorSized.bar = nextBar;
      } else if (e.target === reply) {
        anchorSized.reply = bb ? bb.blockSize : e.contentRect.height;
      }
    });
    anchorApplySizes(col);
    if (anchorSized.bounded !== prevBounded || (anchorSized.bounded && Math.abs(anchorSized.h - prevH) >= 1) || (insetChanged && freeActive && freeWinStart === 0)) {
      if (anchorSized.bounded) anchorSettle(null);
      else if (typeof virtualReconcile === "function") virtualReconcile({ full: true });
      if (typeof boardDialSync === "function") boardDialSync();
    }
    if (insetChanged && anchorProg) anchorRealign(false);
  }
  function anchorCancelProg(e) {
    if (e && e.defaultPrevented && !(e.target && e.target.closest && e.target.closest(".bc-grip"))) return;
    anchorUserScroll++;
    freeReleasePin();
    var col = $("bcol"), sc = null;
    if (freeJump && freeJump.moving) sc = col;
    else if (anchorProg && anchorProg.smooth && !anchorProg.scrollDone) sc = anchorProg.scroller;
    anchorProg = null;
    freeJump = null;
    if (sc && sc.scrollTo) sc.scrollTo({ top: sc.scrollTop, behavior: "instant" });
    if (freeActive && !freeSuspended) freeScheduleIdle(col);
  }
  function anchorOnWheel(e) {
    if (!e.ctrlKey && Math.abs(e.deltaY) > Math.abs(e.deltaX)) anchorCancelProg(e);
  }
  function anchorOnKey(e) {
    var k = e && e.key;
    if (k === "ArrowUp" || k === "ArrowDown" || k === "PageUp" || k === "PageDown" || k === "Home" || k === "End" || k === " ") anchorCancelProg(e);
  }
  function anchorScrollEnd(e) {
    var col = $("bcol");
    if (!col) return;
    if (freeActive) {
      if (e.target === col) {
        freePendingScrollEnd = true;
        if (typeof requestAnimationFrame === "function") {
          if (!freeScrollRaf) freeScrollRaf = requestAnimationFrame(freeScrollTick);
        } else {
          freeScrollTick();
        }
      }
      return;
    }
    var bounded = anchorIsBounded(col), sc = anchorScroller(col, bounded);
    if (e.target !== col && e.target !== sc && !(sc === doc.scrollingElement && (e.target === doc || e.target === doc.documentElement))) return;
    var p = anchorProg;
    if (p && p.key === anchorKey && Date.now() <= p.until) {
      if (!p.smooth && Math.abs(sc.scrollTop - p.top) > 1) { anchorProg = null; }
      else {
        var targetNode = anchorNode(p.key) || anchorLast;
        if (targetNode && targetNode.isConnected) {
          var err = 0, canMove = false;
          if (targetNode.getAttribute && targetNode.getAttribute("data-anchor-cap") === "0") {
            err = -sc.scrollTop;
            canMove = sc.scrollTop > 1;
          } else {
            var c = bounded ? col.getBoundingClientRect() : null, r = targetNode.getBoundingClientRect();
            err = r.top - anchorLandingTop(col, c, targetNode);
            canMove = err > 0 ? sc.scrollTop + sc.clientHeight < sc.scrollHeight - 1 : sc.scrollTop > 1;
          }
          if (Math.abs(err) > 1.5 && canMove && p.tries < 2) {
            p.tries++;
            anchorAlign(col, targetNode, false, bounded);
            return;
          }
        }
        p.scrollDone = true;
        if (!p.waitFloat) anchorProg = null;
        else anchorWaitFrames(p.id, 3);
        return;
      }
    }
    anchorProg = null;
    var list = anchorCards();
    if (!list.length) return;
    var vt = (bounded ? col.getBoundingClientRect().top + (col.clientTop || 0) : 0) + anchorSized.padTop + anchorRailInset(col);
    var rects = list.map(function (n) { return n.getBoundingClientRect(); });
    var railCount = 0, pick = -1, closest = null, gap = Infinity;
    for (var k = 0; k < list.length; k++) {
      var n = list[k], rk = rects[k];
      if (!rk.height) { if (n.hasAttribute("data-topic") || n.hasAttribute("data-rail")) railCount++; continue; }
      var cap = n.hasAttribute("data-topic") || n.hasAttribute("data-rail") ? anchorCapH(Math.min(2, railCount)) : 0;
      if (n.hasAttribute("data-topic") || n.hasAttribute("data-rail")) railCount++;
      var line = vt + cap;
      if (rk.top <= line + 1.5) pick = k;
      var d = Math.abs(rk.top - line);
      if (d < gap) { gap = d; closest = n; }
    }
    var best = pick >= 0 && rects[pick].bottom > vt + anchorCardCap(list[pick], list) + 1.5 ? list[pick] : closest;
    if (best && best !== anchorLast) anchorMark(best);
  }
  function anchorInit() {
    var col = $("bcol");
    if (!col || !region("board")) return;
    var colCs = getComputedStyle(col);
    anchorSized.padTop = parseFloat(colCs.paddingTop) || 0;
    anchorSized.bounded = /auto|scroll/.test(colCs.overflowY);
    anchorSized.h = anchorSized.bounded ? (col.clientHeight || window.innerHeight || 0) : 0;
    anchorSized.inset = anchorRailInset(col);
    col.addEventListener("scroll", freeOnScroll, { passive: true });
    col.addEventListener("wheel", anchorOnWheel, { passive: true });
    col.addEventListener("pointerdown", anchorCancelProg, { passive: true });
    col.addEventListener("touchstart", anchorCancelProg, { passive: true });
    col.addEventListener("touchmove", anchorCancelProg, { passive: true });
    col.addEventListener("keydown", anchorOnKey, { passive: true });
    col.addEventListener("scrollend", anchorScrollEnd, { passive: true });
    col.addEventListener("load", function () { if (freePin) freePinLayout(); }, true);
    if (doc.addEventListener) doc.addEventListener("scrollend", anchorScrollEnd, { passive: true });
    if (window.ResizeObserver) {
      anchorRO = new ResizeObserver(anchorSize);
      anchorRO.observe(col);
      anchorObserveAux(col);
    }
    if (window.MutationObserver) {
      anchorMO = new MutationObserver(function () {
        var nextInset = anchorRailInset(col);
        if (nextInset === anchorSized.inset) return;
        anchorSized.inset = nextInset;
        anchorApplySizes(col);
        if (freeActive && freeWinStart === 0) {
          var moList = anchorCards(), moNode = moList[freeAnchorIdx];
          freeLayout({ anchorIdx: freeAnchorIdx, anchorY: moNode && freePos.has(moNode) ? freePos.get(moNode) : undefined, list: moList });
        }
        if (anchorProg) anchorRealign(true);
      });
      anchorMO.observe(col, { attributes: true, attributeFilter: ["style"] });
      anchorBoardMO = new MutationObserver(anchorProcessRecords);
      anchorBoardMO.observe(region("board"), {
        childList: true,
        subtree: true,
        attributes: true,
        attributeFilter: ["hidden", "data-state-filtered", "data-card", "data-topic", "data-rail", "data-max", "data-has-panel-max", "data-overview-live", "aria-pressed", "data-no"]
      });
    }
    var boardEl = region("board");
    if (boardEl && boardEl.addEventListener) {
      var onBoardExemptEvt = function (e) { virtualMarkExemptDirty(e && e.target); };
      boardEl.addEventListener("input", onBoardExemptEvt, { passive: true });
      boardEl.addEventListener("change", onBoardExemptEvt, { passive: true });
      boardEl.addEventListener("focusout", onBoardExemptEvt, { passive: true });
      boardEl.addEventListener("click", onBoardExemptEvt, { passive: true });
    }
    col.wbScrollOwner = true;
    if (doc && doc.addEventListener && typeof boardRevealInit === "function") boardRevealInit();
    anchorApplySizes(col);
    anchorSettle(null);
    if (!freeActive && typeof virtualReconcile === "function") virtualReconcile({ full: true });
    if (typeof boardDialInit === "function") boardDialInit();
    window.WsBoardAnchor = {
      restore: function () { var n = anchorCurrent(); if (n) anchorSelect(n, { keep: true }); },
      select: anchorSelect,
      land: freeLandTarget,
      pinning: function () { return !!freePinned(); },
      step: anchorStep,
      owner: anchorOwner,
      key: function () { return anchorKey; },
      cards: anchorCards,
      bounded: function () { return !!anchorSized.bounded; },
      free: function () { return !!freeActive; },
      windowInfo: freeWindowInfo,
      visibleCards: anchorVisibleCards,
      releaseNode: freeClearNode,
      suspendForOverview: freeSuspendForOverview,
      resumeFromOverview: freeResumeFromOverview,
      settle: function (opts) {
        anchorSettle(null);
        if (!freeActive && !(opts && opts.skipVirtual) && typeof virtualReconcile === "function") virtualReconcile({ full: true });
        if (typeof boardDialSync === "function") boardDialSync();
      },
      reveal: typeof boardRevealSchedule === "function" ? boardRevealSchedule : null,
      scrubbing: function () { return typeof dialScrubbing !== "undefined" && !!dialScrubbing; },
      scrolling: function () { return !!(freeActive && (freeScrollRaf || freeIdleTimer)); },
      keepFrame: function (t) { return !!(freeActive && (freePos.has(t) || ((freeScrollRaf || freeIdleTimer) && freeDeferredCollapse.has(t)))); },
      hydrate: typeof virtualHydrate === "function" ? virtualHydrate : null,
      hydrateForTarget: typeof virtualHydrateForTarget === "function" ? virtualHydrateForTarget : null,
      reconcile: typeof virtualReconcile === "function" ? virtualReconcile : null,
      virtual: window.WsBoardVirtual || null,
      dialWindow: typeof boardDialWindow === "function" ? boardDialWindow : null,
      dialTier: typeof boardDialTier === "function" ? boardDialTier : null
    };
  }
