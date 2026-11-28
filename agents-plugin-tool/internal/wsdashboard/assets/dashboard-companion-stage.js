(function (C) {
  "use strict";
  const { clamp, mix, finite } = C;
  const landmarks = ["composer-top", "whiteboard-ask", "hud-roster"];
  const dragSnapLandmarks = ["composer-top", "whiteboard-ask", "hud-roster"];
  const RAIL_MIN_WIDTH = 1600;
  const VRM_ACTIVE_FPS = 30;
  const VRM_IDLE_FPS = 6;
  const VRM_IDLE_AFTER_MS = 5000;
  // The companion config payload fields the rig's wardrobe reads.
  const wardrobeFields = ["outfit", "hair", "eyes", "attachments", "palette"];
  const railRowSelector = ".rs[data-rid]";
  let mountedRailObserver, mountedStage = null;

  // phoneSnapNewer reports whether companion phone snapshot next may replace
  // prev: a different daemon epoch (a restarted daemon restarts seq) or a seq
  // no older than prev's. Snapshots without seq are accepted.
  function phoneSnapNewer(prev, next) {
    if (!prev || !next) return !!next;
    if ((prev.epoch || 0) !== (next.epoch || 0)) return true;
    const a = Number(prev.seq), b = Number(next.seq);
    if (!Number.isFinite(a) || !Number.isFinite(b)) return true;
    return b >= a;
  }

  function railSignals() {
    let rows = new Map();

    function read(fragment, activeRid, baseline) {
      const nodes = fragment.querySelectorAll(railRowSelector), next = new Map();
      const childApprovals = new Set();
      for (const row of nodes) {
        if (!row.matches(".ended, .stopped") && row.hasAttribute("data-ap")) {
          const parent = row.closest(".rkids")?.dataset.parent;
          if (parent) childApprovals.add(parent);
        }
      }
      let winner = null;
      function offer(id, kind, priority, order, token) {
        const active = id === activeRid;
        const rank = priority * 4 + (active ? 2 : 0) + order;
        if (!winner || rank > winner.rank) winner = { rid: id, kind, token, rank, source: "rail" };
      }
      for (const row of nodes) {
        const id = row.dataset.rid;
        if (!id || next.has(id) || row.matches(".ended, .stopped")) continue;
        const previous = rows.get(id), busy = row.dataset.busy === "1", top = !row.hasAttribute("data-g");
        const working = row.dataset.active === "1";
        // Match rail notices: keep the session's own work marked until its busy span ends.
        const active = busy && (working || !!(previous?.busy && previous.active));
        const approval = row.hasAttribute("data-ap") || top && !childApprovals.has(id) && !!row.querySelector(".kap");
        const failure = !!row.querySelector(".hb.err");
        const asks = row.dataset.asks === undefined ? null : new Set(row.dataset.asks.trim().split(/\s+/).filter(Boolean));
        next.set(id, { busy, active, working, top, approval, failure, asks });
        if (baseline) continue;
        if (failure && !previous?.failure) offer(id, "session.error", 70, 0);
        if (approval && !previous?.approval) offer(id, "ask.open", 65, 1, "perm:" + id);
        if (asks && previous?.asks) {
          for (const ask of asks) {
            if (!previous.asks.has(ask)) offer(id, "ask.open", 65, 0, "ask:" + id + ":" + ask);
          }
        }
        if (id !== activeRid && top && previous?.top && previous.busy && previous.active && !busy && !approval && !failure) {
          offer(id, "global.session_done", 52, 0);
        }
      }
      rows = next;
      return winner;
    }

    function posture(activeRid, activity) {
      let permission = "", failure = "", working = 0;
      for (const [id, row] of rows) {
        if (row.approval && (!permission || id === activeRid)) permission = id;
        if (row.failure && (!failure || id === activeRid)) failure = id;
        if (id !== activeRid && row.working && !row.approval && !row.failure) working++;
      }
      if (activity === "permission") return { activity, rid: activeRid };
      if (permission) return { activity: "permission", rid: permission };
      if (activity === "error") return { activity, rid: activeRid };
      if (failure) return { activity: "error", rid: failure };
      if (activity === "idle" && working) return { activity: "tool", rid: "" };
      return { activity, rid: "" };
    }
    return { read, posture };
  }

  function signalPairs() {
    const pending = new Map();
    return {
      observe(signal, stamp) {
        if (!/^(?:ask|perm):/.test(signal.token || "")) return false;
        for (const [key, prior] of pending) if (stamp - prior.stamp > 1250) pending.delete(key);
        const prior = pending.get(signal.token);
        if (prior && prior.source !== signal.source) {
          pending.delete(signal.token);
          return true;
        }
        pending.set(signal.token, { source: signal.source, stamp });
        return false;
      },
      clear() { pending.clear(); }
    };
  }

  function resizeControl(grip, options, currentSize, changed, limits = {}) {
    const key = limits.key || "ws.companion.size";
    const minSize = limits.min ?? 64;
    const maxSize = limits.max ?? 240;
    const isActive = limits.active || (() => true);
    const invertX = !!limits.invertX;
    let preferred = null, active = null, cap = maxSize;
    try {
      const saved = window.localStorage.getItem(key);
      const value = Number(saved);
      if (saved !== null && Number.isFinite(value) && value >= minSize && value <= maxSize) preferred = value;
    } catch (_) { /* Storage may be unavailable. */ }
    function persist() {
      try { window.localStorage.setItem(key, String(preferred)); } catch (_) { /* Storage may be unavailable. */ }
    }
    function update(value) {
      preferred = Math.round(clamp(value, Math.min(minSize, cap), cap));
      changed();
    }
    grip.addEventListener("pointerdown", event => {
      if (!isActive() || event.button !== 0 || active) return;
      event.preventDefault();
      active = { id: event.pointerId, x: event.clientX, y: event.clientY, size: currentSize() };
      grip.setPointerCapture(event.pointerId);
    }, options);
    grip.addEventListener("pointermove", event => {
      if (active?.id !== event.pointerId) return;
      const dx = invertX ? active.x - event.clientX : event.clientX - active.x;
      const dy = active.y - event.clientY;
      update(active.size + (Math.abs(dx) >= Math.abs(dy) ? dx : dy));
    }, options);
    function finish(event) {
      if (active?.id !== event.pointerId) return;
      active = null;
      if (preferred !== null) persist();
    }
    for (const name of ["pointerup", "pointercancel", "lostpointercapture"]) grip.addEventListener(name, finish, options);
    grip.addEventListener("keydown", event => {
      if (!isActive()) return;
      const direction = { ArrowRight: 1, ArrowUp: 1, ArrowLeft: -1, ArrowDown: -1 }[event.key];
      if (!direction) return;
      event.preventDefault();
      update(currentSize() + direction * 4);
      persist();
    }, options);
    return {
      isActive() { return !!active; },
      value(fallback, width, height) {
        cap = Math.max(1, Math.min(maxSize, width - 24, height - 24));
        const value = Math.min(preferred ?? fallback, cap);
        grip.setAttribute("aria-valuemin", String(Math.min(minSize, cap)));
        grip.setAttribute("aria-valuemax", String(cap));
        grip.setAttribute("aria-valuenow", String(Math.round(value)));
        return value;
      },
      reset() {
        preferred = null;
        try { window.localStorage.removeItem(key); } catch (_) { /* Storage may be unavailable. */ }
        changed();
      }
    };
  }

  function gearIcon(doc) {
    const ns = "http://www.w3.org/2000/svg";
    const svg = doc.createElementNS(ns, "svg");
    svg.setAttribute("viewBox", "0 0 24 24");
    svg.setAttribute("aria-hidden", "true");
    const teeth = doc.createElementNS(ns, "circle");
    for (const [name, value] of [["cx", "12"], ["cy", "12"], ["r", "8"], ["fill", "none"], ["stroke", "currentColor"],
      ["stroke-width", "4"], ["stroke-dasharray", "3.14 3.14"]]) teeth.setAttribute(name, value);
    const ring = doc.createElementNS(ns, "circle");
    for (const [name, value] of [["cx", "12"], ["cy", "12"], ["r", "5"], ["fill", "none"], ["stroke", "currentColor"],
      ["stroke-width", "2.5"]]) ring.setAttribute(name, value);
    svg.append(teeth, ring);
    return svg;
  }

  function chatIcon(doc) {
    const ns = "http://www.w3.org/2000/svg";
    const svg = doc.createElementNS(ns, "svg");
    svg.setAttribute("viewBox", "0 0 24 24");
    svg.setAttribute("aria-hidden", "true");
    const bubble = doc.createElementNS(ns, "path");
    bubble.setAttribute("d", "M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z");
    bubble.setAttribute("fill", "none");
    bubble.setAttribute("stroke", "currentColor");
    bubble.setAttribute("stroke-width", "2");
    bubble.setAttribute("stroke-linecap", "round");
    bubble.setAttribute("stroke-linejoin", "round");
    svg.append(bubble);
    for (const cx of ["9.2", "12.5", "15.8"]) {
      const dot = doc.createElementNS(ns, "circle");
      dot.setAttribute("cx", cx);
      dot.setAttribute("cy", "11.5");
      dot.setAttribute("r", "1.05");
      dot.setAttribute("fill", "currentColor");
      svg.append(dot);
    }
    return svg;
  }

  function tOr(key, fallback) {
    if (typeof window.wsT === "function") {
      const val = window.wsT(key);
      if (val && val !== key) return val;
    }
    return fallback;
  }

  function mount(config, mute, openSettings) {
    const doc = document, root = doc.documentElement, rail = doc.getElementById("rail");
    const cancel = new AbortController(), options = { signal: cancel.signal };
    const node = doc.createElement("div");
    node.className = "ws-companion";
    const hit = doc.createElement("div");
    hit.className = "ws-companion-hit";
    const character = doc.createElement("button");
    character.type = "button";
    character.className = "ws-companion-character";
    character.title = wsT("js.companion.character");
    character.setAttribute("aria-label", character.title);
    character.setAttribute("aria-haspopup", "true");
    character.setAttribute("aria-expanded", "false");
    const canvas = doc.createElement("canvas");
    canvas.setAttribute("aria-hidden", "true");
    const vrmContainer = doc.createElement("div");
    vrmContainer.className = "ws-companion-vrm-wrap";
    vrmContainer.setAttribute("aria-hidden", "true");
    vrmContainer.hidden = true;
    character.append(canvas, vrmContainer);
    const grip = doc.createElement("div");
    grip.className = "ws-companion-resize";
    grip.tabIndex = 0;
    grip.setAttribute("role", "slider");
    grip.setAttribute("aria-label", wsT("js.companion.resize"));
    grip.setAttribute("aria-valuemin", "64");
    const gear = doc.createElement("button");
    gear.type = "button";
    gear.className = "ws-companion-gear";
    gear.title = wsT("js.companion.settings");
    gear.setAttribute("aria-label", gear.title);
    gear.append(gearIcon(doc));
    if (openSettings) {
      gear.addEventListener("click", () => {
        controls.close();
        openSettings(gear);
      }, options);
    } else {
      gear.hidden = true;
    }
    const chatBtn = doc.createElement("button");
    chatBtn.type = "button";
    chatBtn.id = "companion-chat-btn";
    chatBtn.className = "ws-companion-chat-btn";
    chatBtn.title = tOr("js.companion.messenger.open", "Open companion messenger");
    chatBtn.setAttribute("aria-label", chatBtn.title);
    chatBtn.setAttribute("aria-expanded", "false");
    chatBtn.setAttribute("aria-pressed", "false");
    chatBtn.setAttribute("aria-controls", "companion-messenger");
    chatBtn.append(chatIcon(doc));
    const unreadBadge = doc.createElement("span");
    unreadBadge.className = "ws-companion-unread";
    unreadBadge.hidden = true;
    unreadBadge.setAttribute("aria-live", "polite");
    chatBtn.append(unreadBadge);
    chatBtn.addEventListener("click", event => {
      event.stopPropagation();
      controls.close();
      window.WsCompanionMessenger?.toggle?.(chatBtn);
    }, options);
    const knockFxLayer = doc.createElement("div");
    knockFxLayer.className = "ws-companion-knock-fx";
    knockFxLayer.setAttribute("aria-hidden", "true");
    hit.append(character, knockFxLayer, grip, chatBtn, gear);
    hit.hidden = true;
    const ctx = canvas.getContext("2d");
    const menu = doc.createElement("div");
    menu.className = "ws-companion-menu";
    menu.setAttribute("aria-label", character.title);
    menu.hidden = true;
    node.append(hit, menu);
    doc.body.append(node);

    let model, sharedPack = null, rig, view, runner = C.scenarioRunner(), timing = C.timing(), fxContext = null;
    let state = window.WsShell.companionContext();
    let activeRid = root.dataset.rid || "", posture = { activity: "idle", rid: "" }, switchedAt = -Infinity;
    let markedRid, railMark;
    const railEvents = railSignals(), pairs = signalPairs();
    let currentConfig = config, preset = config.preset;
    let frame = 0, idleTimer = 0, last = 0, now = 0, viewStart = 0, lastBusy = 0, nextIdle = 45000;
    let lastVrmRenderAt = -Infinity, lastVrmActivityAt = 0, vrmIdleMode = false, lastVrmGazeX = 0, lastVrmGazeY = 0;
    let vrmRailVisible = false, cursorSeen = false, lastVrmW = 0, lastVrmH = 0, lastVrmDpr = 0;
    let beat = null, viewBeat, viewPlaying, prop, fx, propCanvas, propCtx, decoration, quip, quipWidth = 0, quipHeight = 28, fxWidth = 36, fxHeight = 24, lastCount;
    let size = 96, vrmHeight = 240, dpr = 1, vw = 0, vh = 0, dirty = true;
    let frameElement = null, frameInput = null, frameBox = null;
    let home = { x: 60, y: 96 }, arrived = false, custom = null, drag = null, suppressClick = false;
    let vrmPointer = null, lastVrmPointer = null, hitArea = "";
    let companionHover = false;
    let latestPhoneSnap = null;
    // Phone gesture received while no rig was mounted; replayed after the
    // latest phone snapshot once a rig mounts unless a newer snapshot came.
    let pendingPhoneGesture = "";
    const wideMq = typeof window.matchMedia === "function" ? window.matchMedia("(min-width: 1600px)") : null;
    const physics = { swing: 0, swingVel: 0 };
    const beatOrigin = { x: 0, y: 0 };
    const position = { x: 0, y: 0 }, cursor = { x: 0, y: 0 }, gaze = { x: 0, y: 0, prop: false };
    const propPosition = { x: 0, y: 0 }, propStart = { x: 0, y: 0 }, propEnd = { x: 0, y: 0 };
    const physical = {}, propFrame = {}, bodyBox = {};
    const guards = { routeKind: "home", pose: "stand", idleMs: 0 };
    let marks = Object.create(null);

    function isVrm() { return rig?.renderer === "vrm"; }
    function markVrmActivity() {
      lastVrmActivityAt = performance.now();
      if (vrmIdleMode) {
        vrmIdleMode = false;
        if (idleTimer) { clearTimeout(idleTimer); idleTimer = 0; }
      }
    }

    // The focus report carries the session this stage shows and the pack it
    // renders; before the first pack is drawn nothing is reported.
    function reportFocus() { C.focus?.show(activeRid, model?.id || ""); }
    function notice(key) { window.WsToast?.show(wsT("js.companion." + key), { kind: "notice" }); }
    const loader = C.packLoader((pack, shared) => {
      const nextIsVrm = pack.renderer === "vrm";
      const makeAdapter = C.makeVrmAdapter || C.makeVrmRig;
      const next = nextIsVrm ? makeAdapter(pack, {
        onReady: () => {
          if (!vrmRailVisible) { next.pause?.(); return; }
          markVrmActivity();
          invalidated();
        },
        onContextLost: () => {
          cancelAnimationFrame(frame);
          if (idleTimer) { clearTimeout(idleTimer); idleTimer = 0; }
          frame = 0;
          last = 0;
          lastVrmRenderAt = -Infinity;
        },
        onContextRestored: () => {
          if (!vrmRailVisible) { next.pause?.(); return; }
          markVrmActivity();
          invalidated();
        }
      }) : C.makeRig(pack, shared, preset, currentConfig);
      // Prepare the neutral body and its tint cache before replacing the active rig.
      next.view("stand", null, "idle", []);
      const prevRig = rig;
      const switchedRenderer = (prevRig?.renderer || "2d") !== (next.renderer || "2d");
      if (prevRig && prevRig !== next && typeof prevRig.dispose === "function") prevRig.dispose();
      model = pack;
      sharedPack = shared || null;
      rig = next;
      replayPhoneToRig();
      runner = C.scenarioRunner(pack.manifest.scenarios);
      timing = C.timing(pack.manifest);
      if (now - switchedAt < 900) runner.fire("route.switch", switchedAt, guards);
      viewBeat = null;
      dirty = true;
      if (switchedRenderer) {
        custom = null;
        arrived = false;
      }
      node.dataset.pack = pack.id;
      node.dataset.rev = pack.rev;
      node.dataset.renderer = nextIsVrm ? "vrm" : "2d";
      canvas.hidden = nextIsVrm;
      vrmContainer.hidden = !nextIsVrm;
      if (propCanvas && nextIsVrm) propCanvas.hidden = true;
      vrmRailVisible = false;
      lastVrmW = 0;
      lastVrmH = 0;
      lastVrmDpr = 0;
      clearRailDock();
      if (nextIsVrm) {
        layout();
        next.mount?.(vrmContainer, { width: Math.max(160, size), height: Math.max(140, vrmHeight), dpr: Math.min(dpr, 2) });
        if (vrmRailVisible) {
          markVrmActivity();
          hit.hidden = false;
        } else {
          next.pause?.();
          hit.hidden = true;
        }
      } else {
        layout();
        hit.hidden = false;
      }
      reportFocus();
      if (!menu.hidden) controls.rebuild();
      schedule();
    }, () => notice("pack_failed"));

    function placeMenu() {
      if (menu.hidden) return;
      controls.measure();
      const charH = isVrm() ? vrmHeight : size;
      const rightEdge = position.x + (bodyBox.width || size) / 2 + 6;
      const bottomEdge = (Number.isFinite(bodyBox.y) ? bodyBox.y : position.y - charH) + (bodyBox.height || charH);
      controls.place(rightEdge, bottomEdge, vw, vh);
    }

    const controls = C.quickMenu(node, character, menu, options, {
      returnHome, resetSize, mute, applyPreset, notice, openSettings,
      onOpen() {
        placeMenu();
        if (isVrm() && vrmRailVisible) { markVrmActivity(); schedule(); }
      },
      presets: () => ({ available: Object.keys(model?.manifest.presets || {}), preset }),
      async reloadPack() {
        await loader.reload(cancel.signal);
        if (!cancel.signal.aborted) requestPack(true);
      }
    });

    // apply takes a live config broadcast. A wardrobe change re-dresses the
    // loaded rig in place; the rig clears its view and tint caches.
    function apply(next) {
      const changed = currentConfig.preset !== next.preset;
      const dressed = wardrobeFields.some(field => currentConfig[field] !== next[field]);
      currentConfig = next;
      if (changed) applyPreset(next.preset);
      if (dressed && rig) {
        rig.setWardrobe(next);
        viewBeat = null;
        schedule();
      }
      requestPack();
    }

    const knockTimers = new Set();

    function replayPhoneToRig() {
      if (!rig) return;
      if (latestPhoneSnap) rig.setPhoneState?.(latestPhoneSnap);
      if (pendingPhoneGesture) {
        const g = pendingPhoneGesture;
        pendingPhoneGesture = "";
        if (isVrm()) rig.triggerGesture?.(g);
      }
    }

    function spawnKnockFx(clientX, clientY, label) {
      if (!knockFxLayer) return;
      const rect = typeof hit.getBoundingClientRect === "function" ? hit.getBoundingClientRect() : null;
      const relX = rect && Number.isFinite(clientX) && (clientX !== 0 || clientY !== 0)
        ? clamp(clientX - rect.left, 18, Math.max(18, (rect.width || size) - 18))
        : Math.round((rect?.width || size) * 0.5);
      const relY = rect && Number.isFinite(clientY) && (clientX !== 0 || clientY !== 0)
        ? clamp(clientY - rect.top, 22, Math.max(22, (rect.height || vrmHeight) - 22))
        : Math.round((rect?.height || vrmHeight) * 0.42);
      const r1 = doc.createElement("span");
      r1.className = "ws-knock-ring";
      r1.style.left = relX + "px";
      r1.style.top = relY + "px";
      const r2 = doc.createElement("span");
      r2.className = "ws-knock-ring ring-2";
      r2.style.left = relX + "px";
      r2.style.top = relY + "px";
      const word = doc.createElement("span");
      word.className = "ws-knock-word";
      word.textContent = label || tOr("js.companion.knock.cue", "톡톡");
      word.style.left = relX + "px";
      word.style.top = Math.max(18, relY - 8) + "px";
      knockFxLayer.append(r1, r2, word);
      const tid = setTimeout(() => {
        knockTimers.delete(tid);
        r1.remove?.();
        r2.remove?.();
        word.remove?.();
      }, 840);
      knockTimers.add(tid);
    }

    function updateUnreadBadge(count) {
      const n = Number.isFinite(count) ? Math.max(0, Math.floor(count)) : 0;
      unreadBadge.hidden = n <= 0;
      unreadBadge.textContent = n > 9 ? "9+" : (n > 0 ? String(n) : "");
      chatBtn.dataset.unread = n > 0 ? "1" : "0";
    }

    function phoneState(snap) {
      if (!C.record(snap)) return;
      // Monotonic seq guard: a late HTTP /thread or /mark-read answer carrying
      // an older phone_active snapshot never overwrites a newer putdown/idle.
      if (!phoneSnapNewer(latestPhoneSnap, snap)) return;
      latestPhoneSnap = snap;
      updateUnreadBadge(snap.unreadClient || 0);
      if (rig?.setPhoneState) {
        rig.setPhoneState(snap);
      } else if (pendingPhoneGesture) {
        // A newer authoritative phone snapshot supersedes a phone gesture
        // buffered while no rig was mounted.
        pendingPhoneGesture = "";
      }
      if (isVrm() && vrmRailVisible && (snap.state === "phone_active" || (snap.unreadAgent || 0) > 0 || snap.phoneFaceDown)) {
        markVrmActivity();
        schedule();
      }
    }

    // command plays an agent-director command for the shown session over the
    // rules engine until its TTL ends. Wildcard "*" targets the active shell
    // companion regardless of focused session tab (used by phone/MCP tools).
    function command(payload) {
      if (!C.record(payload)) return false;
      if (payload.sessionId !== "*" && (!activeRid || payload.sessionId !== activeRid)) return false;
      if (!rig) {
        // Buffer phone choreography until a rig mounts so pickup/putdown are
        // never dropped while the pack loads.
        if (typeof payload.gesture === "string" && payload.gesture.startsWith("phone_")) {
          pendingPhoneGesture = payload.gesture;
          return true;
        }
        return false;
      }
      const directed = C.directedCommand(payload, model, sharedPack, runner);
      if (isVrm() && typeof payload.gesture === "string" && payload.gesture) {
        rig.triggerGesture?.(payload.gesture);
      }
      if (!directed) {
        if (isVrm() && typeof payload.gesture === "string" && payload.gesture) {
          markVrmActivity();
          schedule();
          return true;
        }
        return false;
      }
      runner.direct(directed.beats, now, directed.ttl, directed.id);
      viewBeat = null;
      if (isVrm()) markVrmActivity();
      schedule();
      return true;
    }

    function applyPreset(id) {
      if (preset === id) return;
      preset = id;
      rig?.setPreset(id);
      viewBeat = null;
      if (!menu.hidden) controls.rebuild();
      schedule();
    }

    function requestPack(force) {
      loader.load(currentConfig.pack || "default", state, force);
    }

    function reset(id, switched) {
      if (custom?.mark) custom = { x: position.x, y: position.y };
      activeRid = id || "";
      state = window.WsShell.companionContext(activeRid ? "session" : "home");
      marks = Object.create(null);
      runner.reset();
      pairs.clear();
      viewBeat = null;
      nextIdle = now + 45000;
      lastBusy = now;
      switchedAt = switched ? now : -Infinity;
      if (switched) fire("route.switch");
      updatePosture();
      requestPack();
      invalidated();
      reportFocus();
    }

    function context(payload, frameRect) {
      const previous = state.activity;
      const previousComposer = marks["composer-top"];
      state = payload.activity === "streaming" ? { ...payload, activity: "replying" } : payload;
      frameBox = frameRect;
      marks = Object.create(null);
      for (const id of landmarks) {
        const m = payload.landmarks[id];
        if (!m || ![m.x, m.y, m.width, m.height].every(Number.isFinite) || m.width <= 0 || m.height <= 0) continue;
        const left = frameRect.left + m.x;
        const top = frameRect.top + m.y;
        marks[id] = { left, top, right: left + m.width, bottom: top + m.height, width: m.width, height: m.height, x: left + m.width / 2, y: top };
      }
      const composer = marks["composer-top"];
      if (composer?.left !== previousComposer?.left || composer?.y !== previousComposer?.y) {
        dirty = true;
      }
      requestPack();
      if (previous === "replying" && state.activity !== "replying") runner.endReply();
      updatePosture();
      for (const event of payload.events) {
        if (!event || typeof event.kind !== "string") continue;
        const token = typeof event.token === "string" ? event.token : "";
        const rid = token.startsWith("perm:") ? token.slice(5) : payload.sessionId;
        receiveSignal({ kind: event.kind, token, rid, source: "frame" });
      }
      if (payload.events.length || previous !== state.activity) markVrmActivity();
      schedule();
    }

    function updatePosture() {
      const next = railEvents.posture(activeRid, state.activity);
      if (next.activity !== posture.activity) { lastBusy = now; nextIdle = now + 45000; }
      posture = next;
    }

    function receiveSignal(signal) {
      if (pairs.observe(signal, performance.now())) {
        if (signal.source === "frame") runner.preferFrame(signal);
      } else fire(signal.kind, signal);
    }

    function observeRail(fragment, id, baseline) {
      activeRid = id || "";
      const signal = railEvents.read(fragment, activeRid, baseline);
      reportFocus();
      updatePosture();
      if (signal) receiveSignal(signal);
      schedule();
    }

    function fire(kind, signal) {
      guards.routeKind = state.routeKind;
      guards.pose = view?.pose || "stand";
      guards.idleMs = posture.activity === "idle" ? now - lastBusy : 0;
      runner.fire(kind, now, guards, signal);
      if (kind.startsWith("user.")) {
        lastBusy = now;
        nextIdle = now + 45000;
      }
    }

    const sizing = resizeControl(grip, options, () => size, invalidated, { active: () => !isVrm() });

    function resetSize() {
      if (!isVrm()) sizing.reset();
      controls.close();
    }

    function packsChanged() { requestPack(true); }

    function setHitArea(area) {
      const next = area || "";
      if (hitArea === next) return;
      hitArea = next;
      if (next) hit.dataset.area = next;
      else hit.removeAttribute("data-area");
    }

    function hitTestVrmPoint(clientX, clientY) {
      if (!isVrm() || !vrmRailVisible || hit.hidden) return null;
      if (!Number.isFinite(clientX) || !Number.isFinite(clientY)) return null;
      const bx = Number.isFinite(bodyBox.x) ? bodyBox.x : home.x - size / 2;
      const by = Number.isFinite(bodyBox.y) ? bodyBox.y : home.y - vrmHeight;
      const lx = clientX - bx;
      const ly = clientY - by;
      const bounds = rig?.getCharacterBounds?.();
      if (!bounds || !Number.isFinite(bounds.x) || !Number.isFinite(bounds.y) ||
          !Number.isFinite(bounds.width) || !Number.isFinite(bounds.height) ||
          bounds.width <= 0 || bounds.height <= 0) {
        return "messenger";
      }
      const inside = lx >= bounds.x && lx <= bounds.x + bounds.width &&
        ly >= bounds.y && ly <= bounds.y + bounds.height;
      return inside ? "character" : "messenger";
    }

    function isCharacterSurfaceTarget(el) {
      return !!(el && (el === character || character.contains(el) || el === hit));
    }

    function trackVrmPointerMove(event) {
      if (!vrmPointer || (Number.isFinite(vrmPointer.id) && event.pointerId !== vrmPointer.id)) return;
      if (!vrmPointer.moved && Math.hypot(event.clientX - vrmPointer.startX, event.clientY - vrmPointer.startY) > 6) {
        vrmPointer.moved = true;
      }
    }

    function finishVrmPointerUp(event) {
      if (!vrmPointer || (Number.isFinite(vrmPointer.id) && event.pointerId !== vrmPointer.id)) return;
      const endX = Number.isFinite(event.clientX) ? event.clientX : vrmPointer.startX;
      const endY = Number.isFinite(event.clientY) ? event.clientY : vrmPointer.startY;
      const dist = Math.hypot(endX - vrmPointer.startX, endY - vrmPointer.startY);
      lastVrmPointer = {
        moved: vrmPointer.moved || dist > 6,
        downArea: vrmPointer.downArea,
        button: vrmPointer.button
      };
      vrmPointer = null;
    }

    function cancelVrmPointer(event) {
      if (!vrmPointer || (Number.isFinite(vrmPointer.id) && event.pointerId !== vrmPointer.id)) return;
      vrmPointer = null;
      lastVrmPointer = { moved: true, downArea: null, button: 0 };
    }

    function isCompanionTarget(el) {
      return !!(el && el !== node && node.contains(el));
    }

    function setCompanionHover(next) {
      const flag = !!next;
      if (companionHover === flag) return;
      companionHover = flag;
      node.dataset.hover = flag ? "1" : "0";
      if (isVrm() && vrmRailVisible) {
        markVrmActivity();
        schedule();
      }
    }

    let beforeUnreg = null;
    function setBeforeUnreg(el) {
      if (beforeUnreg === el) return;
      beforeUnreg?.removeAttribute("data-companion-before-unreg");
      beforeUnreg = el || null;
      beforeUnreg?.setAttribute("data-companion-before-unreg", "");
    }

    function clearRailDock() {
      rail?.removeAttribute("data-companion-dock");
      setBeforeUnreg(null);
      rail?.style.removeProperty("--companion-slot");
      const slotEl = rail?.querySelector("#rail-companion-slot");
      if (slotEl) slotEl.hidden = true;
    }

    function hideVrmForNarrow() {
      clearRailDock();
      vrmRailVisible = false;
      vrmPointer = null;
      lastVrmPointer = null;
      setHitArea("");
      node.dataset.dock = "hidden";
      node.dataset.avoiding = "0";
      hit.hidden = true;
      controls.close();
      if (decoration) decoration.hidden = true;
      if (quip) quip.hidden = true;
      if (frame) { cancelAnimationFrame(frame); frame = 0; }
      if (idleTimer) { clearTimeout(idleTimer); idleTimer = 0; }
      last = 0;
      lastVrmRenderAt = -Infinity;
      rig?.pause?.();
    }

    function computeRailVrmHeight(railRect) {
      const railH = Math.max(0, railRect?.height || vh);
      let targetH = clamp(Math.round(vh * 0.255), 184, 256);
      if (railH > 0) {
        targetH = Math.min(targetH, Math.max(152, Math.round(railH * 0.34)));
      }
      return Math.round(targetH);
    }

    function layout() {
      dirty = false;
      markedRid = undefined;
      vw = window.innerWidth;
      vh = window.innerHeight;
      dpr = window.devicePixelRatio || 1;
      frameBox = frameElement?.getBoundingClientRect();
      if (!menu.hidden) controls.measure();
      if (isVrm()) {
        const wide = vw >= RAIL_MIN_WIDTH && (!wideMq || wideMq.matches) && root?.getAttribute?.("data-rail-collapsed") !== "true";
        const railRect = rail?.getBoundingClientRect();
        const slotEl = rail?.querySelector("#rail-companion-slot");
        const railStyle = rail && typeof getComputedStyle === "function" ? getComputedStyle(rail) : null;
        const railShown = !!(wide && rail && slotEl && railRect && railRect.width > 0 && railRect.height > 0 &&
          (!railStyle || (railStyle.visibility !== "hidden" && railStyle.display !== "none")));
        if (!railShown) {
          hideVrmForNarrow();
          home = { x: 0, y: 0, vrm: true, rail: false, hidden: true, avoiding: false };
          return;
        }
        const wasVisible = vrmRailVisible;
        vrmRailVisible = true;
        custom = null;
        drag = null;
        const targetH = computeRailVrmHeight(railRect);
        const slotCss = targetH + "px";
        if (rail.style.getPropertyValue("--companion-slot") !== slotCss) rail.style.setProperty("--companion-slot", slotCss);
        if (rail.dataset.companionDock !== "rail-slot") rail.dataset.companionDock = "rail-slot";
        setBeforeUnreg(null);
        if (slotEl.hidden) slotEl.hidden = false;
        const slotRect = slotEl.getBoundingClientRect();
        const nextW = Math.max(160, Math.round(slotRect.width || railRect.width));
        const nextH = Math.max(140, Math.round(slotRect.height || targetH));
        const nextDpr = Math.min(dpr, 2);
        const prevHomeX = home.x;
        const prevHomeY = home.y;
        let slotChanged = false;
        size = nextW;
        vrmHeight = nextH;
        if (nextW !== lastVrmW || nextH !== lastVrmH || nextDpr !== lastVrmDpr) {
          lastVrmW = nextW;
          lastVrmH = nextH;
          lastVrmDpr = nextDpr;
          rig?.resize?.(size, vrmHeight, nextDpr);
          slotChanged = true;
        }
        const anchorLeft = Number.isFinite(slotRect.left) && slotRect.width > 0 ? slotRect.left : railRect.left;
        const anchorBottom = Number.isFinite(slotRect.bottom) && slotRect.height > 0 ? slotRect.bottom : railRect.bottom;
        home = { x: anchorLeft + size / 2, y: anchorBottom, vrm: true, rail: true, hidden: false, avoiding: false };
        if (Math.abs(home.x - prevHomeX) > 0.5 || Math.abs(home.y - prevHomeY) > 0.5) slotChanged = true;
        position.x = home.x;
        position.y = home.y;
        arrived = true;
        node.dataset.dock = "rail-slot";
        node.dataset.avoiding = "0";
        if (slotChanged && wasVisible && !doc.hidden) {
          markVrmActivity();
          lastVrmRenderAt = -Infinity;
        }
        if (!wasVisible) {
          hit.hidden = false;
          if (!doc.hidden) {
            rig?.resume?.();
            markVrmActivity();
          }
        }
        if (latestPhoneSnap) rig?.setPhoneState?.(latestPhoneSnap);
        window.WsCompanionMessenger?.reposition?.();
        return;
      }
      node.dataset.avoiding = "0";
      setHitArea("");
      const slotEl = rail?.querySelector("#rail-companion-slot");
      if (slotEl && !slotEl.hidden) slotEl.hidden = true;
      const rect = rail?.getBoundingClientRect();
      const open = vw >= RAIL_MIN_WIDTH || root.classList.contains("rail-open");
      const railStyle = rail && typeof getComputedStyle === "function" ? getComputedStyle(rail) : null;
      const docked = rect && open && rect.right > 0 && (!railStyle || railStyle.visibility !== "hidden");
      if (!docked) {
        node.dataset.dock = "viewport";
        rail?.removeAttribute("data-companion-dock");
        setBeforeUnreg(null);
        size = sizing.value(clamp(Math.min(vw - 24, vh - 24), 48, 104), vw, vh);
        const composer = marks["composer-top"];
        home = composer ? {
          x: clamp(composer.left + size / 2, 12 + size / 2, vw - size / 2),
          y: clamp(composer.y - 2, size, vh - 12)
        } : { x: 12 + size / 2, y: vh - 12, corner: true };
        return;
      }

      node.dataset.dock = "rail-2d";
      size = sizing.value(clamp(Math.min(rect.width * 0.44, rect.height * 0.36), 88, 152), vw, vh);
      const unreg = rail.querySelector(".rws-unreg");
      const footer = rail.querySelector(".rfoot, .r-config-global");
      const reserve = !custom && !drag;
      const box = unreg?.getBoundingClientRect();
      const slot = Math.ceil(size) + 4;
      const hasUnreg = !!(unreg && box && box.height > 0);
      rail.style.setProperty("--companion-slot", slot + "px");
      setBeforeUnreg(null);
      if (reserve) {
        rail.dataset.companionDock = hasUnreg ? "unregistered" : "footer";
      } else {
        rail.removeAttribute("data-companion-dock");
      }
      const divider = (hasUnreg ? unreg : footer)?.getBoundingClientRect().top ?? rect.bottom;
      home = { x: rect.left + rect.width / 2, y: divider - 2, rail: true };
    }

    function invalidated() {
      dirty = true;
      if (isVrm() && !doc.hidden && !cancel.signal.aborted) {
        const wideNow = window.innerWidth >= RAIL_MIN_WIDTH && (!wideMq || wideMq.matches) && root?.getAttribute?.("data-rail-collapsed") !== "true";
        if (wideNow !== vrmRailVisible) {
          layout();
        } else if (vrmRailVisible) {
          const slotEl = rail?.querySelector("#rail-companion-slot");
          const r = slotEl && !slotEl.hidden ? slotEl.getBoundingClientRect() : null;
          if (r && r.width > 0 && r.height > 0) {
            const w = Math.max(160, Math.round(r.width));
            const h = Math.max(140, Math.round(r.height));
            if (w !== lastVrmW || h !== lastVrmH ||
                Math.abs(r.left + w / 2 - home.x) > 0.5 || Math.abs(r.bottom - home.y) > 0.5) {
              markVrmActivity();
              lastVrmRenderAt = -Infinity;
            }
          }
        }
      }
      schedule();
    }
    const railResize = new ResizeObserver(invalidated);
    let observed = [];
    function observeRailLayout() {
      const nodes = rail ? [rail, ...rail.querySelectorAll(".rail-session-panel, .rlist, .rfoot, .rws-unreg, #rail-companion-slot")] : [];
      if (nodes.length === observed.length && nodes.every((item, i) => item === observed[i])) return;
      railResize.disconnect();
      nodes.forEach(el => railResize.observe(el));
      observed = nodes;
    }
    const railChanges = new MutationObserver(() => { observeRailLayout(); invalidated(); });
    if (rail) {
      railChanges.observe(rail, { childList: true, subtree: true, attributes: true, attributeFilter: ["open", "class"] });
      rail.addEventListener("scroll", invalidated, { ...options, capture: true });
      rail.addEventListener("transitionend", invalidated, options);
      observeRailLayout();
    }
    railChanges.observe(root, { attributes: true, attributeFilter: ["class", "data-rail-collapsed"] });
    window.addEventListener("resize", invalidated, options);
    wideMq?.addEventListener?.("change", invalidated, options);

    function target() {
      return custom?.mark ? marks[custom.mark] || custom : custom || home;
    }

    function railLandmark(id) {
      if (markedRid === id) return railMark;
      markedRid = id;
      railMark = null;
      if (!id || !rail) return railMark;
      const row = rail.querySelector('.rs[data-rid="' + CSS.escape(id) + '"]');
      let rect = row?.getBoundingClientRect();
      const parent = row?.closest(".rkids")?.previousElementSibling;
      if ((!rect?.width || !rect.height) && parent?.matches(railRowSelector)) rect = parent.getBoundingClientRect();
      if (rect?.width && rect.height && rect.right > 0 && rect.bottom > 0 && rect.left < vw && rect.top < vh) {
        railMark = { x: rect.left + rect.width / 2, y: rect.top };
      }
      return railMark;
    }

    function gazeMark(key, playing) {
      const signal = playing?.signal;
      const rid = signal?.rid || (!playing ? posture.rid : "");
      if (key === "rail-alert") return railLandmark(rid);
      if (key === "whiteboard-ask" && rid) {
        return (signal?.source === "frame" || rid === activeRid ? marks[key] : null) || railLandmark(rid);
      }
      return C.own(marks, key === "caret" ? "composer-top" : key);
    }

    function measureCharacter() {
      if (isVrm()) {
        bodyBox.width = size;
        bodyBox.height = vrmHeight;
        bodyBox.x = position.x + physical.tx * size - size / 2;
        bodyBox.y = position.y + physical.lift * vrmHeight - vrmHeight;
        return;
      }
      const v = view, b = v.bounds;
      const width = (b[2] - b[0]) * size, height = (b[3] - b[1]) * size;
      const perching = v.pose === "perched" && (home.rail || custom?.mark);
      const bottom = perching ? v.seat ?? b[3] : b[3];
      bodyBox.width = width;
      bodyBox.height = height;
      bodyBox.x = position.x + physical.tx * size - width / 2;
      bodyBox.y = position.y + (physical.lift + b[1] - bottom) * size;
    }

    function placeCharacter(seconds, extra) {
      const { x, y, width, height } = bodyBox, b = view.bounds;
      if (isVrm()) {
        rig.setGaze?.(gaze);
        view.render(seconds, gaze, beat.stage === "pendulum-drag" ? physics : null, extra);
        hit.style.width = width + "px";
        hit.style.height = height + "px";
        hit.style.transform = "translate3d(" + x + "px," + y + "px,0) rotate(" + physical.angle + "rad) scale(" + physical.sx + "," + physical.sy + ")";
        controls.place(position.x + width / 2 + 6, y + height, vw, vh);
        return;
      }
      const widthPixels = Math.ceil(size * dpr);
      if (canvas.width !== widthPixels || canvas.height !== widthPixels) canvas.width = canvas.height = widthPixels;
      const art = view.render(seconds, gaze, beat.stage === "pendulum-drag" && view.articulatedDrag ? physics : null);
      ctx.clearRect(0, 0, widthPixels, widthPixels);
      ctx.drawImage(art, 0, 0, widthPixels, widthPixels);
      hit.style.width = width + "px";
      hit.style.height = height + "px";
      hit.style.transform = "translate3d(" + x + "px," + y + "px,0) rotate(" + physical.angle + "rad) scale(" + physical.sx + "," + physical.sy + ")";
      canvas.style.width = canvas.style.height = size + "px";
      canvas.style.left = -b[0] * size + "px";
      canvas.style.top = -b[1] * size + "px";
      controls.place(position.x + width / 2 + 6, y + height, vw, vh);
    }

    function propAction(item) { return typeof item === "string" ? item : item?.id; }

    function propBehavior(selector, data) {
      const motion = selector?.motion || data.behavior || "attach";
      return C.own(timing.prop, motion) ? motion : C.own(timing.prop, data.behavior) ? data.behavior : "attach";
    }

    function setBeat(next, playing) {
      viewBeat = next;
      viewPlaying = playing;
      beat = next;
      beatOrigin.x = position.x;
      beatOrigin.y = position.y;
      viewStart = now;
      const propId = propAction(next.prop);
      const attach = rig.prop(propId);
      const attached = attach.data && propBehavior(next.prop, attach.data) === "attach" &&
        (!attach.data.anchor || attach.data.anchor === "behind-head" || attach.data.anchor === "body");
      const renderPose = next.pose || "stand";
      view = rig.view(renderPose, next.gesture, next.expression || "idle", attached ? [propId] : []);
      markVrmActivity();
      node.dataset.pose = view.pose;
      node.dataset.scenario = playing?.entry.id || "";
      prop = !attached && attach.data ? attach : null;
      const rawFx = rig.prop(propAction(next.fx), "fx");
      fx = rawFx?.data ? rawFx : { ...(rawFx || { drawable: false, draw() {} }), data: C.own(C.fxDefaults, propAction(next.fx)) };
      lastCount = null;
      if (quip) quip.hidden = !next.quip;
      if (next.quip) {
        if (!quip) {
          quip = doc.createElement("div");
          quip.className = "ws-companion-quip";
          node.append(quip);
        }
        // Text only: a quip may come from a model and must never become markup.
        quip.textContent = next.quip;
        quipWidth = quip.offsetWidth || 0;
        quipHeight = quip.offsetHeight || 28;
      }
    }

    function handPosition(out) {
      const ox = bodyBox.width * 0.5, oy = bodyBox.height * 0.8;
      const x = (size * (view.handAnchor[0] / rig.width - view.bounds[0]) - ox) * physical.sx;
      const y = (size * (view.handAnchor[1] / rig.height - view.bounds[1]) - oy) * physical.sy;
      const cos = Math.cos(physical.angle), sin = Math.sin(physical.angle);
      out.x = bodyBox.x + ox + x * cos - y * sin;
      out.y = bodyBox.y + oy + x * sin + y * cos;
    }

    function propPoint(out, id, homeSide) {
      if (id === "hand") { handPosition(out); return; }
      const mark = C.own(marks, id), rest = target();
      out.x = mark?.x ?? (homeSide ? position.x : rest.x + size * 1.25);
      out.y = mark?.y ?? (homeSide ? position.y - size * 0.52 : rest.y - size * 0.8);
    }

    function propRecoil(progress) {
      if (!prop) return;
      const recoil = timing.prop[propBehavior(beat.prop, prop.data)].recoil;
      if (!recoil || progress <= recoil.start || progress >= recoil.end) return;
      const envelope = progress <= recoil.peak ? (progress - recoil.start) / (recoil.peak - recoil.start) :
        (recoil.end - progress) / (recoil.end - recoil.peak);
      physical.lift += envelope * (recoil.lift || 0);
      physical.angle += envelope * (recoil.angle || 0) * (position.x < vw / 2 ? 1 : -1);
    }

    function positionProp(progress, age) {
      if (!prop) {
        if (propCanvas) propCanvas.hidden = true;
        return;
      }
      if (!propCanvas) {
        propCanvas = doc.createElement("canvas");
        propCanvas.className = "ws-companion-prop";
        propCanvas.setAttribute("aria-hidden", "true");
        node.insertBefore(propCanvas, menu);
        propCtx = propCanvas.getContext("2d");
      }
      const behavior = propBehavior(beat.prop, prop.data), curve = timing.prop[behavior];
      const hand = behavior === "hand-liftoff" ? "hand" : undefined;
      const returning = beat.prop?.action === "return";
      const destination = beat.prop?.to || prop.data.anchor;
      propPoint(propStart, beat.prop?.from || (returning ? destination : hand), !returning);
      propPoint(propEnd, beat.prop?.to || (returning ? hand : destination), returning ||
        behavior === "burst" || !!curve.edge);
      if (curve.edge) {
        const right = curve.edge === "right" || curve.edge === "away" && position.x < vw / 2;
        propStart.x = right ? vw + size / 2 : -size / 2;
        propStart.y = clamp(propEnd.y - size * 1.1, 12, vh - 12);
      }
      curve.sample(progress, propFrame);
      propPosition.x = mix(propStart.x, propEnd.x, propFrame.along) + size * propFrame.offsetX;
      propPosition.y = mix(propStart.y, propEnd.y, propFrame.along) + size * (propFrame.arcY + propFrame.offsetY);
      const pixels = Math.ceil(dpr * size);
      if (propCanvas.width !== pixels || propCanvas.height !== pixels) propCanvas.width = propCanvas.height = pixels;
      propCtx.setTransform(pixels / rig.width, 0, 0, pixels / rig.height, 0, 0);
      propCtx.clearRect(0, 0, rig.width, rig.height);
      prop.draw(propCtx, age);
      propCanvas.hidden = false;
      propCanvas.style.opacity = String(propFrame.alpha);
      propCanvas.style.width = propCanvas.style.height = size + "px";
      propCanvas.style.transform = "translate3d(" + (propPosition.x - size / 2) + "px," + (propPosition.y - size / 2) +
        "px,0) rotate(" + propFrame.rotDeg + "deg) scale(" + propFrame.scale + ")";
    }

    function effects(playing, progress, age) {
      const def = fx.data, count = playing?.count || 1;
      if (!playing) progress = def?.kind === "float" ? age % 2.3 / 2.3 : 0;
      if (!def && count < 2) {
        if (decoration) decoration.hidden = true;
      } else {
        if (!decoration) {
          decoration = doc.createElement("div");
          decoration.className = "ws-companion-fx";
          decoration.setAttribute("aria-hidden", "true");
          node.insertBefore(decoration, menu);
        }
        if (lastCount !== count) {
          lastCount = count;
          decoration.replaceChildren();
          if (fx.drawable) {
            const image = doc.createElement("canvas");
            image.width = image.height = 96;
            decoration.append(image);
            fxContext = image.getContext("2d");
          } else {
            fxContext = null;
            const glyphs = def?.kind === "particles" && Array.isArray(def.glyphs) ? def.glyphs : [def?.text || ""];
            for (const text of glyphs.slice(0, 8)) {
              const icon = doc.createElement("span");
              icon.textContent = typeof text === "string" ? text : "";
              decoration.append(icon);
            }
          }
          if (count > 1) decoration.append("×" + count);
          decoration.style.color = typeof def?.color === "string" ? def.color : "";
          decoration.dataset.kind = def?.kind || "badge";
          decoration.hidden = false;
          fxWidth = decoration.offsetWidth || (fxContext ? 96 : 36);
          fxHeight = decoration.offsetHeight || (fxContext ? 96 : 24);
        }
        if (fxContext) {
          fxContext.setTransform(96 / rig.width, 0, 0, 96 / rig.height, 0, 0);
          fxContext.clearRect(0, 0, rig.width, rig.height);
          fx.draw(fxContext, age);
        }
        decoration.hidden = false;
        const charH = isVrm() ? vrmHeight : size;
        const padX = def?.kind === "particles" ? 36 : 8;
        const padTop = def?.kind === "particles" ? 18 : 8;
        const fxW = fxWidth || 36;
        const fxH = fxHeight || 24;
        let fxX, fxY;
        if (isVrm()) {
          // Keep FX badges neatly perched near the upper-right of the bust,
          // anchored in document.body so .rail { overflow: hidden } never clips them.
          const minX = Math.max(padX, bodyBox.x + 12);
          const maxX = Math.max(minX, Math.min(vw - fxW - padX, bodyBox.x + bodyBox.width + 24));
          fxX = clamp(position.x + size * 0.24, minX, maxX);
          const rawY = bodyBox.y + Math.round(charH * 0.14) - Math.round(Math.min(24, charH * 0.09) * progress);
          const minY = Math.max(padTop, bodyBox.y - 12);
          const maxY = Math.max(minY, Math.min(vh - fxH - 8, bodyBox.y + bodyBox.height - fxH - 8));
          fxY = clamp(rawY, minY, maxY);
        } else {
          fxX = clamp(position.x + size * 0.26, padX, Math.max(padX, vw - fxW - padX));
          const rawY = position.y - charH * (0.77 + progress * 0.36);
          fxY = clamp(rawY, padTop, Math.max(padTop, vh - fxH - 8));
        }
        decoration.style.transform = "translate3d(" + Math.round(fxX) + "px," + Math.round(fxY) + "px,0)";
        decoration.style.opacity = String(Math.min(1, (1 - progress) * 4, !playing && progress ? progress * 12 : 1));
        if (def?.kind === "particles") {
          const nodes = decoration.children;
          for (let i = 0; i < nodes.length; i++) {
            nodes[i].style.transform = "translate(" + (i - (nodes.length - 1) / 2) * progress * 52 +
              "px," + (-Math.sin(progress * Math.PI) * (10 + i * 3)) + "px)";
          }
        }
      }
      if (quip && !quip.hidden) {
        const charH = isVrm() ? vrmHeight : size;
        const left = clamp(position.x - quipWidth / 2, 6, Math.max(6, vw - quipWidth - 6));
        const top = isVrm()
          ? clamp(bodyBox.y - (quipHeight || 28) - 4, 6, Math.max(6, vh - (quipHeight || 28) - 6))
          : clamp(position.y - charH * 0.95, 6, Math.max(6, vh - (quipHeight || 28) - 6));
        quip.style.transform = "translate3d(" + Math.round(left) + "px," + Math.round(top) + "px,0)";
      }
    }

    function look(age, dt, playing) {
      const key = beat.gaze;
      const charH = isVrm() ? vrmHeight : size;
      let x = isVrm() && home.rail && !cursorSeen ? 0.24 : (cursor.x - position.x) / 150;
      let y = isVrm() && home.rail && !cursorSeen ? -0.06 : (cursor.y - position.y + charH * 0.6) / 120;
      const mark = gazeMark(key, playing);
      gaze.prop = !!prop && key === "prop:" + propAction(beat.prop);
      if (gaze.prop) {
        x = (propPosition.x - position.x) / size;
        y = (propPosition.y - position.y + charH * 0.61) / size;
      } else if (mark) {
        x = (mark.x - position.x) / 160;
        y = (mark.y - position.y + charH * 0.61) / 160;
      } else if (key === "scan") { x = Math.sin(age * 3); y = 0.2; }
      else if (key === "up" || key === "down") { x = 0; y = key === "up" ? -1 : 1; }
      const easing = 1 - Math.exp(-dt / (gaze.prop ? 36 : 75));
      gaze.x = mix(gaze.x, clamp(x, -1, 1), easing);
      gaze.y = mix(gaze.y, clamp(y, -1, 1), easing);
    }

    function animate(stamp) {
      frame = 0;
      if (doc.hidden || cancel.signal.aborted || rig?.isContextLost?.()) return;
      if (!rig) return;
      if (dirty) layout();
      if (isVrm() && !vrmRailVisible) return;
      if (isVrm()) {
        const co = rig?.getCoLeadState?.();
        const coActive = !!(co && (co.knockActive || co.swapActive || co.dizzyActive || (co.phoneMode && co.phoneMode !== "desk")));
        const activeNow = !!viewPlaying || !!drag?.moving || posture.activity !== "idle" || coActive ||
          Math.abs(position.x - target().x) > 0.5 || Math.abs(position.y - target().y) > 0.5;
        if (activeNow) markVrmActivity();
        const idleElapsed = performance.now() - lastVrmActivityAt;
        vrmIdleMode = !activeNow && idleElapsed >= VRM_IDLE_AFTER_MS;
        const targetFps = vrmIdleMode ? VRM_IDLE_FPS : VRM_ACTIVE_FPS;
        const minInterval = 1000 / targetFps;
        if (lastVrmRenderAt > -Infinity && stamp - lastVrmRenderAt < minInterval - 4) {
          schedule(stamp);
          return;
        }
        lastVrmRenderAt = stamp;
      }
      const dt = last ? clamp(stamp - last, 0, isVrm() && vrmIdleMode ? 200 : 50) : 16;
      now += dt;
      last = stamp;
      if (posture.activity !== "idle" || drag) nextIdle = now + 45000;
      else if (now >= nextIdle) { fire("idle.long"); nextIdle = now + 45000; }
      const playing = runner.tick(now, !!drag?.moving);
      const beats = timing.passive;
      const idleMs = now - lastBusy;
      const dozing = posture.activity === "idle" && idleMs > 45000 && (idleMs - 45000) % 45000 < 32000;
      const passive = dozing ? beats.doze : C.own(beats, posture.activity) || beats.idle;
      const next = playing?.beat || passive;
      if (viewBeat !== next || viewPlaying !== playing) setBeat(next, playing);
      const rest = target();
      let x = rest.x, y = rest.y;
      if (rest.corner) x = 12 + ((view.bounds[2] - view.bounds[0]) * size) / 2;
      if (drag?.moving) { x = drag.x; y = drag.y; }
      const age = (now - viewStart) / 1000;
      const progress = playing?.beat.ms ? clamp((now - playing.start) / playing.beat.ms, 0, 1) : playing ? 0 : age % 2.3 / 2.3;
      const mark = !isVrm() && playing && next.stage === "hop-bounce" && gazeMark(next.gaze, playing);
      if (mark && !drag?.moving) {
        const toward = progress * progress * (3 - 2 * progress);
        x = mix(beatOrigin.x, clamp(mark.x, size / 2, vw - size / 2), toward);
        y = mix(beatOrigin.y, clamp(mark.y, size, vh - 12), toward);
      }
      const amount = arrived && !drag?.moving && !(isVrm() && home.rail) ? 1 - Math.exp(-dt / 85) : 1;
      position.x = mix(position.x, x, amount);
      position.y = mix(position.y, y, amount);
      arrived = true;
      const { stiffness, damping, maxSwing, bodyFallbackScale } = timing.drag;
      // Substep the spring so the upper pack damping bound stays stable at
      // the largest frame interval we accept after a stall.
      const steps = Math.ceil(dt / 16) || 1, step = dt / (1000 * steps);
      for (let i = 0; i < steps; i++) {
        physics.swingVel += (-physics.swing * stiffness - physics.swingVel * damping) * step;
        physics.swing = clamp(physics.swing + physics.swingVel * step, -maxSwing, maxSwing);
      }
      const pendulum = next.stage === "pendulum-drag";
      if (isVrm()) {
        // Ground-zero VRM baseline: stay calmly grounded with zero vertical
        // hopping/bouncing, squash/stretch, or prop recoil.
        physical.tx = 0;
        physical.lift = 0;
        physical.sx = 1;
        physical.sy = 1;
        physical.rotDeg = 0;
        physical.angle = 0;
      } else {
        (C.own(timing.stage, next.stage) || timing.stage.stay).sample(progress, physical);
        physical.angle = physical.rotDeg * C.RAD + (pendulum && !view.articulatedDrag ? physics.swing * bodyFallbackScale : 0);
        propRecoil(progress);
      }
      measureCharacter();
      positionProp(progress, age);
      look(age, dt, playing);
      if (isVrm() && (Math.abs(gaze.x - lastVrmGazeX) > 0.015 || Math.abs(gaze.y - lastVrmGazeY) > 0.015)) {
        lastVrmGazeX = gaze.x;
        lastVrmGazeY = gaze.y;
        markVrmActivity();
      }
      const vrmExtra = isVrm() ? {
        progress,
        age,
        beat: next,
        playing,
        position: { x: position.x, y: position.y },
        marks,
        viewport: { width: vw, height: vh }
      } : null;
      placeCharacter(age, vrmExtra);
      effects(playing, progress, age);
      schedule(stamp);
    }

    function schedule(stamp) {
      if (frame || idleTimer || !rig || doc.hidden || cancel.signal.aborted || rig?.isContextLost?.() || (isVrm() && !vrmRailVisible)) return;
      if (isVrm() && vrmIdleMode) {
        const nowMs = typeof stamp === "number" ? stamp : performance.now();
        const elapsedSinceRender = lastVrmRenderAt > -Infinity ? Math.max(0, nowMs - lastVrmRenderAt) : 1000 / VRM_IDLE_FPS;
        const remaining = (1000 / VRM_IDLE_FPS) - elapsedSinceRender;
        if (remaining <= 20) {
          frame = requestAnimationFrame(animate);
          return;
        }
        const delay = Math.max(16, Math.round(remaining - 10));
        idleTimer = setTimeout(() => {
          idleTimer = 0;
          if (!frame && rig && !doc.hidden && !cancel.signal.aborted && (!isVrm() || vrmRailVisible)) frame = requestAnimationFrame(animate);
        }, delay);
        return;
      }
      frame = requestAnimationFrame(animate);
    }
    function captureCursor() {
      frameInput?.abort();
      frameInput = null;
      const body = frameElement?.contentDocument;
      if (doc.hidden || !body) return;
      frameInput = new AbortController();
      body.addEventListener("pointermove", event => {
        cursorSeen = true;
        cursor.x = event.clientX + (frameBox?.left || 0);
        cursor.y = event.clientY + (frameBox?.top || 0);
        setCompanionHover(false);
        if (isVrm() && vrmRailVisible) {
          markVrmActivity();
          schedule();
        }
      }, { signal: frameInput.signal, passive: true });
    }
    function trackFrame(element) {
      frameElement = element;
      frameBox = element?.getBoundingClientRect();
      captureCursor();
    }
    doc.addEventListener("visibilitychange", () => {
      captureCursor();
      if (doc.hidden) {
        cancelAnimationFrame(frame);
        if (idleTimer) { clearTimeout(idleTimer); idleTimer = 0; }
        frame = 0;
        last = 0;
        lastVrmRenderAt = -Infinity;
        rig?.pause?.();
      } else {
        if (!isVrm() || vrmRailVisible) {
          rig?.resume?.();
          markVrmActivity();
        }
        invalidated();
      }
    }, options);
    node.addEventListener("pointerover", event => {
      if (isCompanionTarget(event.target)) setCompanionHover(true);
    }, { ...options, passive: true });
    node.addEventListener("pointerout", event => {
      if (!isCompanionTarget(event.relatedTarget)) {
        setCompanionHover(false);
        setHitArea("");
      }
    }, { ...options, passive: true });
    doc.addEventListener("pointermove", event => {
      cursorSeen = true;
      cursor.x = event.clientX;
      cursor.y = event.clientY;
      trackVrmPointerMove(event);
      setCompanionHover(isCompanionTarget(event.target));
      if (isVrm() && vrmRailVisible) {
        if (isCharacterSurfaceTarget(event.target)) {
          setHitArea(hitTestVrmPoint(event.clientX, event.clientY));
        } else {
          setHitArea("");
        }
        markVrmActivity();
        schedule();
      } else {
        setHitArea("");
      }
    }, { ...options, passive: true });
    doc.addEventListener("pointerup", finishVrmPointerUp, { ...options, passive: true });
    doc.addEventListener("pointercancel", cancelVrmPointer, { ...options, passive: true });
    doc.documentElement?.addEventListener?.("pointerleave", () => {
      setCompanionHover(false);
      setHitArea("");
    }, { ...options, passive: true });

    function returnHome() {
      custom = null;
      drag = null;
      runner.clear();
      controls.close();
      invalidated();
    }
    character.addEventListener("pointerdown", event => {
      if (event.button !== 0) return;
      controls.close();
      suppressClick = false;
      if (isVrm() && home.rail) {
        vrmPointer = {
          id: event.pointerId,
          button: event.button,
          startX: event.clientX,
          startY: event.clientY,
          moved: false,
          downArea: hitTestVrmPoint(event.clientX, event.clientY)
        };
        lastVrmPointer = null;
        return;
      }
      drag = { id: event.pointerId, startX: event.clientX, startY: event.clientY, x: position.x, y: position.y, lastX: event.clientX };
      character.setPointerCapture(event.pointerId);
    }, options);
    character.addEventListener("pointermove", event => {
      if (isVrm() && home.rail) {
        trackVrmPointerMove(event);
        setHitArea(hitTestVrmPoint(event.clientX, event.clientY));
        return;
      }
      if (drag?.id !== event.pointerId) return;
      const dx = event.clientX - drag.startX, dy = event.clientY - drag.startY;
      if (!drag.moving) {
        if (Math.hypot(dx, dy) < 5) return;
        drag.moving = true;
        drag.originX = position.x;
        drag.originY = position.y;
        fire("user.drag");
        dirty = true;
      }
      drag.x = clamp(drag.originX + dx, size / 3, vw - size / 3);
      drag.y = clamp(drag.originY + dy, size / 2, vh - 8);
      if (timing.drag.maxSwing) {
        physics.swingVel = clamp(physics.swingVel - (event.clientX - drag.lastX) * timing.drag.impulse, -5, 5);
      }
      drag.lastX = event.clientX;
    }, options);
    function drop(event) {
      if (isVrm() && home.rail) {
        if (event.type === "pointerup") finishVrmPointerUp(event);
        else cancelVrmPointer(event);
        return;
      }
      if (drag?.id !== event.pointerId) return;
      if (drag.moving) {
        suppressClick = true;
        let distance = Math.hypot(home.x - drag.x, home.y - drag.y), mark = "";
        for (const id of dragSnapLandmarks) {
          const m = marks[id];
          if (!m) continue;
          const d = Math.hypot(m.x - drag.x, m.y - drag.y);
          if (d < distance) { distance = d; mark = id; }
        }
        custom = distance < size * 0.65 ? (mark ? { mark, x: drag.x, y: drag.y } : null) : { x: drag.x, y: drag.y };
        runner.clear();
        fire("session.idle");
      }
      drag = null;
      markVrmActivity();
      invalidated();
    }
    for (const name of ["pointerup", "pointercancel", "lostpointercapture"]) character.addEventListener(name, drop, options);
    character.addEventListener("click", event => {
      const ptr = lastVrmPointer;
      lastVrmPointer = null;
      if (!suppressClick) {
        markVrmActivity();
        if (isVrm() && home.rail) {
          if (event?.button !== undefined && event.button !== 0) return;
          if (ptr && (ptr.moved || ptr.button !== 0)) return;
          const hasCoords = Number.isFinite(event?.clientX) && Number.isFinite(event?.clientY) &&
            (event.clientX !== 0 || event.clientY !== 0 || !!ptr);
          const area = ptr ? ptr.downArea : (hasCoords ? hitTestVrmPoint(event.clientX, event.clientY) : "character");
          if (area === "messenger") {
            controls.close();
            window.WsCompanionMessenger?.toggle?.(chatBtn);
            schedule();
            return;
          }
          if (area !== "character") return;
          // Pure cosmetic glass knock: NEVER opens the messenger popup or menu.
          const co = rig?.getCoLeadState?.();
          const isSleepy = beat?.expression === "idle_doze" || co?.mode === "sleepy" || co?.sleepyWeight > 0.35 ||
            latestPhoneSnap?.state === "laptop_sleepy";
          const res = rig?.triggerKnock?.({ sleepy: isSleepy });
          const label = res?.wokeFromSleepy
            ? tOr("js.companion.knock.wakeup", "화들짝!")
            : res?.kind === "annoyed_tap"
              ? tOr("js.companion.knock.annoyed", "콕!")
              : tOr("js.companion.knock.cue", "톡톡");
          spawnKnockFx(event?.clientX, event?.clientY, label);
          if (typeof fetch === "function") {
            fetch("/api/companion/knock", {
              method: "POST",
              credentials: "same-origin",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({
                kind: typeof res?.kind === "string" && res.kind ? res.kind : "knock_startle",
                count: Number.isFinite(res?.count) && res.count > 0 ? res.count : 1
              })
            }).catch(() => {});
          }
          schedule();
        } else {
          fire("user.poke");
        }
      }
      suppressClick = false;
    }, options);
    character.addEventListener("dblclick", () => { markVrmActivity(); returnHome(); }, options);

    if (window.WsCompanionMessenger && typeof window.WsCompanionMessenger.mount === "function") {
      window.WsCompanionMessenger.mount({
        getAnchorRect() {
          const slotEl = rail?.querySelector("#rail-companion-slot");
          if (slotEl && !slotEl.hidden && typeof slotEl.getBoundingClientRect === "function") {
            const r = slotEl.getBoundingClientRect();
            if (r && r.width > 0 && r.height > 0) return r;
          }
          return typeof hit.getBoundingClientRect === "function" ? hit.getBoundingClientRect() : null;
        },
        getChatButton() { return chatBtn; },
        isToggleTarget(target, event) {
          if (!isVrm() || !vrmRailVisible || hit.hidden) return false;
          if (!isCharacterSurfaceTarget(target)) return false;
          const x = event?.clientX, y = event?.clientY;
          if (!Number.isFinite(x) || !Number.isFinite(y)) return false;
          return hitTestVrmPoint(x, y) === "messenger";
        },
        openSettings: openSettings || null,
        onOpenChange(isOpen) {
          C.messengerPresence?.set?.(isOpen);
          chatBtn.setAttribute("aria-expanded", isOpen ? "true" : "false");
          chatBtn.setAttribute("aria-pressed", isOpen ? "true" : "false");
          if (isVrm() && vrmRailVisible) {
            markVrmActivity();
            schedule();
          }
        },
        onSnapshot(snap) {
          phoneState(snap);
        }
      });
    }

    mountedRailObserver = observeRail;
    if (rail) observeRail(rail, activeRid, true);
    requestPack();
    const api = {
      apply, reset, context, trackFrame, command, phoneState, closeMenu: controls.close,
      packsChanged, resetSize, returnHome,
      debugState() {
        const slotEl = rail?.querySelector("#rail-companion-slot");
        const slotRect = slotEl && !slotEl.hidden ? slotEl.getBoundingClientRect() : null;
        return {
          renderer: rig?.renderer || "2d",
          pack: model?.id || "",
          dock: node.dataset.dock || "",
          hidden: hit.hidden,
          vrmRailVisible,
          size,
          vrmHeight,
          home: { ...home },
          position: { ...position },
          railSlotRect: slotRect ? { left: slotRect.left, top: slotRect.top, right: slotRect.right, bottom: slotRect.bottom, width: slotRect.width, height: slotRect.height } : null,
          avoiding: false,
          companionHover,
          vrmIdleMode,
          unreadClient: latestPhoneSnap?.unreadClient || 0,
          messengerOpen: !!window.WsCompanionMessenger?.isOpen?.(),
          vrm: rig?.debugState?.() || null,
          projectedBounds: isVrm() ? (rig?.getCharacterBounds?.() || null) : null
        };
      },
      dispose() {
        if (mountedRailObserver === observeRail) mountedRailObserver = null;
        if (mountedStage === api) mountedStage = null;
        for (const tid of knockTimers) clearTimeout(tid);
        knockTimers.clear();
        window.WsCompanionMessenger?.dispose?.();
        C.messengerPresence?.set?.(false);
        cancel.abort();
        frameInput?.abort();
        cancelAnimationFrame(frame);
        if (idleTimer) { clearTimeout(idleTimer); idleTimer = 0; }
        if (typeof rig?.dispose === "function") rig.dispose();
        rig = null;
        railChanges.disconnect();
        railResize.disconnect();
        clearRailDock();
        loader.dispose();
        node.remove();
        C.focus?.show("", "");
      }
    };
    mountedStage = api;
    return api;
  }

  Object.assign(C, { mount, railSignals, signalPairs, resizeControl, phoneSnapNewer,
    debugStage() { return mountedStage?.debugState?.() || null; },
    phoneState(snap) { mountedStage?.phoneState?.(snap); },
    toggleMessenger() { return window.WsCompanionMessenger?.toggle?.() ?? false; },
    openMessenger() { return window.WsCompanionMessenger?.open?.() ?? false; },
    closeMessenger() { return window.WsCompanionMessenger?.close?.() ?? false; },
    hasRailObserver() { return !!mountedRailObserver; },
    observeRail(fragment, activeRid, baseline) { mountedRailObserver?.(fragment, activeRid, baseline); } });
})(window.WsCompanion);
