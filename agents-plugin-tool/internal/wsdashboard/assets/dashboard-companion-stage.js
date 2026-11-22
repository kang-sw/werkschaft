(function (C) {
  "use strict";
  const { clamp, mix, finite } = C;
  const landmarks = ["composer-top", "whiteboard-ask", "hud-roster"];
  const fxDefaults = {
    alert: { kind: "badge", text: "!", color: "#e2582c" },
    ponder: { kind: "float", text: "…", color: "#8fa0b8" },
    shock: { kind: "badge", text: "!?", color: "#e2582c" },
    sparkle: { kind: "particles", glyphs: ["✦", "✧", "✦"], color: "#e6aa34" },
    zzz: { kind: "float", text: "Zzz", color: "#8fa0b8" }
  };

  function mount(config, mute) {
    const doc = document, root = doc.documentElement, rail = doc.getElementById("rail");
    const cancel = new AbortController(), options = { signal: cancel.signal };
    const node = doc.createElement("div");
    node.className = "ws-companion";
    const hit = doc.createElement("button");
    hit.type = "button";
    hit.className = "ws-companion-hit";
    hit.title = wsT("js.companion.character");
    hit.setAttribute("aria-label", hit.title);
    hit.setAttribute("aria-haspopup", "true");
    hit.setAttribute("aria-expanded", "false");
    const canvas = doc.createElement("canvas");
    canvas.setAttribute("aria-hidden", "true");
    hit.append(canvas);
    hit.hidden = true;
    const ctx = canvas.getContext("2d");
    const menu = doc.createElement("div");
    menu.className = "ws-companion-menu";
    menu.setAttribute("aria-label", hit.title);
    menu.hidden = true;
    node.append(hit, menu);
    doc.body.append(node);

    let model, rig, view, runner = C.scenarioRunner(), fxContext = null;
    let state = window.WsShell.companionContext();
    let currentConfig = config, preset = config.preset;
    let frame = 0, last = 0, now = 0, viewStart = 0, lastBusy = 0, nextIdle = 45000;
    let beat = null, viewBeat, viewPlaying, prop, fx, propCanvas, propCtx, decoration, quip, lastCount;
    let size = 96, dpr = 1, vw = 0, vh = 0, dirty = true;
    let frameElement = null, frameInput = null, frameBox = null;
    let home = { x: 60, y: 96 }, arrived = false, custom = null, drag = null, suppressClick = false;
    let swing = 0, swingSpeed = 0;
    const beatOrigin = { x: 0, y: 0 };
    const position = { x: 0, y: 0 }, cursor = { x: 0, y: 0 }, gaze = { x: 0, y: 0 };
    const propPosition = { x: 0, y: 0 }, propStart = { x: 0, y: 0 }, propEnd = { x: 0, y: 0 };
    const physical = { sx: 1, sy: 1, angle: 0, lift: 0 };
    const guards = { routeKind: "home", pose: "stand", idleMs: 0 };
    let marks = Object.create(null);

    function notice(key) { window.WsToast?.show(wsT("js.companion." + key), { kind: "notice" }); }
    const loader = C.packLoader((pack, shared) => {
      const next = C.makeRig(pack, shared, preset);
      // Prepare the neutral body and its tint cache before replacing the active rig.
      next.view("stand", null, "idle", []);
      model = pack;
      rig = next;
      runner = C.scenarioRunner(pack.manifest.scenarios);
      viewBeat = null;
      dirty = true;
      node.dataset.pack = pack.id;
      node.dataset.rev = pack.rev;
      hit.hidden = false;
      if (!menu.hidden) controls.rebuild();
      schedule();
    }, () => notice("pack_failed"));

    const controls = C.quickMenu(node, hit, menu, options, {
      returnHome, mute, applyPreset, notice,
      presets: () => ({ available: Object.keys(model?.manifest.presets || {}), preset }),
      async reloadPack() {
        await loader.reload(cancel.signal);
        if (!cancel.signal.aborted) requestPack(true);
      }
    });

    function apply(next) {
      const changed = currentConfig.preset !== next.preset;
      currentConfig = next;
      if (changed) applyPreset(next.preset);
      requestPack();
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

    function reset() {
      if (custom?.mark) custom = { x: position.x, y: position.y };
      state = window.WsShell.companionContext();
      marks = Object.create(null);
      runner.reset();
      viewBeat = null;
      nextIdle = now + 45000;
      lastBusy = now;
      requestPack();
      schedule();
    }

    function context(payload, frameRect) {
      const previous = state.activity;
      state = payload;
      frameBox = frameRect;
      marks = Object.create(null);
      for (const id of landmarks) {
        const m = payload.landmarks[id];
        if (!m || ![m.x, m.y, m.width, m.height].every(Number.isFinite) || m.width <= 0 || m.height <= 0) continue;
        marks[id] = { x: frameRect.left + m.x + m.width / 2, y: frameRect.top + m.y };
      }
      requestPack();
      if (previous !== state.activity) { lastBusy = now; nextIdle = now + 45000; }
      for (const event of payload.events) if (event && typeof event.kind === "string") fire(event.kind);
      schedule();
    }

    function fire(kind) {
      guards.routeKind = state.routeKind;
      guards.pose = view?.pose || "stand";
      guards.idleMs = state.activity === "idle" ? now - lastBusy : 0;
      runner.fire(kind, now, guards);
      if (kind.startsWith("user.")) {
        lastBusy = now;
        nextIdle = now + 45000;
      }
    }

    function layout() {
      dirty = false;
      vw = window.innerWidth;
      vh = window.innerHeight;
      dpr = window.devicePixelRatio || 1;
      frameBox = frameElement?.getBoundingClientRect();
      if (!menu.hidden) controls.measure();
      const rect = rail?.getBoundingClientRect();
      const open = vw >= 1600 || root.classList.contains("rail-open");
      const docked = rect && open && rect.right > 0 && getComputedStyle(rail).visibility !== "hidden";
      if (!docked) {
        rail?.removeAttribute("data-companion-dock");
        size = clamp(Math.min(vw - 24, vh - 24), 48, 96);
        home = { x: 12 + size / 2, y: vh - 12, corner: true };
        return;
      }

      size = clamp(Math.min(rect.width * 0.36, rect.height * 0.36), 72, 136);
      const list = rail.querySelector(".rlist");
      const unreg = rail.querySelector(".rws-unreg > summary, .rws-unreg:not(details)");
      const footer = rail.querySelector(".rfoot, .r-config-global");
      const reserve = !custom && !drag;
      const current = rail.dataset.companionDock;
      const box = unreg?.getBoundingClientRect(), scroller = list?.getBoundingClientRect();
      const slot = Math.ceil(size) + 4;
      const unregTop = box?.top + (reserve && current !== "unregistered" ? slot : 0);
      const listBottom = scroller?.bottom + (current === "footer" ? slot : 0);
      const visible = box?.height > 0 && unregTop - size >= scroller?.top && unregTop < listBottom;
      rail.style.setProperty("--companion-slot", slot + "px");
      if (reserve) rail.dataset.companionDock = visible ? "unregistered" : "footer";
      else rail.removeAttribute("data-companion-dock");
      const divider = (visible ? unreg : footer)?.getBoundingClientRect().top ?? rect.bottom;
      home = { x: rect.left + rect.width / 2, y: divider - 2, corner: false };
    }

    function invalidated() { dirty = true; schedule(); }
    const railResize = new ResizeObserver(invalidated);
    let observed = [];
    function observeRail() {
      const nodes = rail ? [rail, ...rail.querySelectorAll(".rlist, .rfoot, .rws-unreg")] : [];
      if (nodes.length === observed.length && nodes.every((item, i) => item === observed[i])) return;
      railResize.disconnect();
      nodes.forEach(el => railResize.observe(el));
      observed = nodes;
    }
    const railChanges = new MutationObserver(() => { observeRail(); invalidated(); });
    if (rail) {
      railChanges.observe(rail, { childList: true, subtree: true, attributes: true, attributeFilter: ["open"] });
      rail.addEventListener("scroll", invalidated, { ...options, capture: true });
      rail.addEventListener("transitionend", invalidated, options);
      observeRail();
    }
    railChanges.observe(root, { attributes: true, attributeFilter: ["class"] });
    window.addEventListener("resize", invalidated, options);

    function target() {
      return custom?.mark ? marks[custom.mark] || custom : custom || home;
    }

    function placeCharacter(seconds) {
      const v = view, b = v.bounds;
      const width = (b[2] - b[0]) * size, height = (b[3] - b[1]) * size;
      const perching = v.pose === "perched" && (!home.corner || custom?.mark);
      const bottom = perching ? v.seat ?? b[3] : b[3];
      const x = position.x - width / 2;
      const y = position.y + physical.lift + (b[1] - bottom) * size;
      const widthPixels = Math.ceil(size * dpr);
      if (canvas.width !== widthPixels || canvas.height !== widthPixels) canvas.width = canvas.height = widthPixels;
      const art = v.render(seconds, gaze);
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

    function setBeat(next, playing) {
      viewBeat = next;
      viewPlaying = playing;
      beat = next;
      beatOrigin.x = position.x;
      beatOrigin.y = position.y;
      viewStart = now;
      const propId = propAction(next.prop);
      const attach = rig.prop(propId);
      const attached = attach.data && (attach.data.behavior || "attach") === "attach" &&
        (!attach.data.anchor || attach.data.anchor === "behind-head" || attach.data.anchor === "body");
      const renderPose = next.pose || "stand";
      view = rig.view(renderPose, next.gesture, next.expression || "idle", attached ? [propId] : []);
      node.dataset.pose = view.pose;
      node.dataset.scenario = playing?.entry.id || "";
      prop = !attached && attach.data ? attach : null;
      fx = rig.prop(propAction(next.fx), "fx");
      if (!fx.data) fx.data = C.own(fxDefaults, propAction(next.fx));
      lastCount = null;
      if (quip) quip.hidden = !next.quip;
      if (next.quip) {
        if (!quip) {
          quip = doc.createElement("div");
          quip.className = "ws-companion-quip";
          node.append(quip);
        }
        quip.textContent = next.quip;
      }
    }

    function positionProp(playing, progress, age) {
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
      const rest = target();
      const id = beat.prop?.to || beat.prop?.from || prop.data.anchor;
      const mark = C.own(marks, id);
      const sx = position.x, sy = position.y - 0.52 * size;
      propStart.x = sx; propStart.y = sy;
      propEnd.x = mark?.x ?? rest.x + size * 1.25;
      propEnd.y = mark?.y ?? rest.y - size * 0.8;
      const behavior = prop.data.behavior || "attach";
      if (behavior === "bonk-from-edge") {
        propStart.x = sx < vw / 2 ? vw + size / 2 : -size / 2;
        propStart.y = clamp(sy - size * 1.1, 12, vh - 12);
        propEnd.x = sx; propEnd.y = sy;
        if (progress >= 0.27 && progress < 0.30) physical.lift = Math.min(physical.lift, -size * 0.04);
        else if (progress >= 0.30 && progress < 0.58) physical.angle += 0.24 * Math.sin((progress - 0.3) * Math.PI / 0.28);
      } else if (behavior === "burst") {
        propEnd.x = sx; propEnd.y = sy;
      }
      if (beat.prop?.action === "return") {
        C.own(C.propMotion, behavior)?.(progress, age, propEnd, propStart, size, propPosition);
      } else {
        (C.own(C.propMotion, behavior) || C.propMotion.attach)(progress, age, propStart, propEnd, size, propPosition);
      }
      const pixels = Math.ceil(dpr * size);
      if (propCanvas.width !== pixels || propCanvas.height !== pixels) propCanvas.width = propCanvas.height = pixels;
      propCtx.setTransform(pixels / rig.width, 0, 0, pixels / rig.height, 0, 0);
      propCtx.clearRect(0, 0, rig.width, rig.height);
      prop.draw(propCtx, age);
      propCanvas.hidden = false;
      propCanvas.style.width = propCanvas.style.height = size + "px";
      propCanvas.style.transform = "translate3d(" + (propPosition.x - size / 2) + "px," + (propPosition.y - size / 2) + "px,0)";
    }

    function effects(playing, progress, age) {
      const def = fx.data, count = playing?.count || 1;
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
          if (def?.file || def?.layers?.length || def?.frames) {
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
        }
        if (fxContext) {
          fxContext.setTransform(96 / rig.width, 0, 0, 96 / rig.height, 0, 0);
          fxContext.clearRect(0, 0, rig.width, rig.height);
          fx.draw(fxContext, age);
        }
        decoration.hidden = false;
        decoration.style.transform = "translate3d(" + (position.x + size * 0.26) + "px," +
          (position.y - size * (0.77 + progress * 0.36)) + "px,0)";
        decoration.style.opacity = String(Math.min(1, (1 - progress) * 4));
        if (def?.kind === "particles") {
          const nodes = decoration.children;
          for (let i = 0; i < nodes.length; i++) {
            nodes[i].style.transform = "translate(" + (i - (nodes.length - 1) / 2) * progress * 52 +
              "px," + (-Math.sin(progress * Math.PI) * (10 + i * 3)) + "px)";
          }
        }
      }
      if (quip && !quip.hidden) quip.style.transform = "translate3d(" + position.x + "px," + (position.y - size * 0.95) + "px,0)";
    }

    function look(age, dt) {
      const key = beat.gaze;
      let x = (cursor.x - position.x) / 150, y = (cursor.y - position.y + size * 0.6) / 120;
      const mark = C.own(marks, key === "caret" ? "composer-top" : key);
      if (prop && key?.startsWith("prop:")) {
        x = (propPosition.x - position.x) / size;
        y = (propPosition.y - position.y + size * 0.61) / size;
      } else if (mark) {
        x = (mark.x - position.x) / 160;
        y = (mark.y - position.y + size * 0.61) / 160;
      } else if (key === "scan") { x = Math.sin(age * 3); y = 0.2; }
      else if (key === "up" || key === "down") { x = 0; y = key === "up" ? -1 : 1; }
      const easing = 1 - Math.exp(-dt / 75);
      gaze.x = mix(gaze.x, clamp(x, -1, 1), easing);
      gaze.y = mix(gaze.y, clamp(y, -1, 1), easing);
    }

    function animate(stamp) {
      frame = 0;
      if (doc.hidden || cancel.signal.aborted) return;
      const dt = last ? clamp(stamp - last, 0, 50) : 16;
      now += dt;
      last = stamp;
      if (!rig) return;
      if (dirty) layout();
      if (state.activity !== "idle" || drag) nextIdle = now + 45000;
      else if (now >= nextIdle) { fire("idle.long"); nextIdle = now + 45000; }
      const playing = runner.tick(now, !!drag?.moving);
      const passive = state.activity === "idle" && now - lastBusy > 45000 ? C.passive.doze : C.own(C.passive, state.activity) || C.passive.idle;
      const next = playing?.beat || passive;
      if (viewBeat !== next || viewPlaying !== playing) setBeat(next, playing);
      const rest = target();
      let x = rest.x, y = rest.y;
      if (rest.corner) x = 12 + ((view.bounds[2] - view.bounds[0]) * size) / 2;
      if (drag?.moving) { x = drag.x; y = drag.y; }
      const age = (now - viewStart) / 1000;
      const progress = playing?.beat.ms ? clamp((now - playing.start) / playing.beat.ms, 0, 1) : 0;
      const mark = next.stage === "hop-bounce" && C.own(marks, next.gaze);
      if (mark && !drag?.moving) {
        const toward = progress * progress * (3 - 2 * progress);
        x = mix(beatOrigin.x, clamp(mark.x, size / 2, vw - size / 2), toward);
        y = mix(beatOrigin.y, clamp(mark.y, size, vh - 12), toward);
      }
      const amount = arrived && !drag?.moving ? 1 - Math.exp(-dt / 85) : 1;
      position.x = mix(position.x, x, amount);
      position.y = mix(position.y, y, amount);
      arrived = true;
      swingSpeed += (-swing * 45 - swingSpeed * 8) * dt / 1000;
      swing = clamp(swing + swingSpeed * dt / 1000, -0.5, 0.5);
      C.stageEffects.stay(progress, age, size, physical);
      (C.own(C.stageEffects, next.stage) || C.stageEffects.stay)(progress, age, size, physical, swing);
      positionProp(playing, progress, age);
      look(age, dt);
      placeCharacter(age);
      effects(playing, progress, age);
      schedule();
    }

    function schedule() {
      if (!frame && rig && !doc.hidden && !cancel.signal.aborted) frame = requestAnimationFrame(animate);
    }
    function captureCursor() {
      frameInput?.abort();
      frameInput = null;
      const body = frameElement?.contentDocument;
      if (doc.hidden || !body) return;
      frameInput = new AbortController();
      body.addEventListener("pointermove", event => {
        cursor.x = event.clientX + (frameBox?.left || 0);
        cursor.y = event.clientY + (frameBox?.top || 0);
      }, { signal: frameInput.signal, passive: true });
    }
    function trackFrame(element) {
      frameElement = element;
      frameBox = element?.getBoundingClientRect();
      captureCursor();
    }
    doc.addEventListener("visibilitychange", () => {
      captureCursor();
      if (doc.hidden) { cancelAnimationFrame(frame); frame = 0; last = 0; }
      else invalidated();
    }, options);
    doc.addEventListener("pointermove", event => {
      cursor.x = event.clientX;
      cursor.y = event.clientY;
    }, { ...options, passive: true });

    function returnHome() {
      custom = null;
      drag = null;
      runner.clear();
      controls.close();
      invalidated();
    }
    hit.addEventListener("pointerdown", event => {
      if (event.button !== 0) return;
      controls.close();
      suppressClick = false;
      drag = { id: event.pointerId, startX: event.clientX, startY: event.clientY, x: position.x, y: position.y, lastX: event.clientX };
      hit.setPointerCapture(event.pointerId);
    }, options);
    hit.addEventListener("pointermove", event => {
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
      swingSpeed = clamp(swingSpeed - (event.clientX - drag.lastX) * 0.085, -5, 5);
      drag.lastX = event.clientX;
    }, options);
    function drop(event) {
      if (drag?.id !== event.pointerId) return;
      if (drag.moving) {
        suppressClick = true;
        let distance = Math.hypot(home.x - drag.x, home.y - drag.y), mark = "";
        for (const id of landmarks) {
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
      invalidated();
    }
    for (const name of ["pointerup", "pointercancel", "lostpointercapture"]) hit.addEventListener(name, drop, options);
    hit.addEventListener("click", () => { if (!suppressClick) fire("user.poke"); suppressClick = false; }, options);
    hit.addEventListener("dblclick", returnHome, options);

    requestPack();
    return {
      apply, reset, context, trackFrame, closeMenu: controls.close,
      dispose() {
        cancel.abort();
        frameInput?.abort();
        cancelAnimationFrame(frame);
        railChanges.disconnect();
        railResize.disconnect();
        rail?.removeAttribute("data-companion-dock");
        rail?.style.removeProperty("--companion-slot");
        loader.dispose();
        node.remove();
      }
    };
  }

  C.mount = mount;
})(window.WsCompanion);
