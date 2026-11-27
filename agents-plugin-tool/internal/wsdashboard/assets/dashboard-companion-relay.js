  // A framed view creates its observer only while the outer shell opts in.
  function startCompanionRelay() {
    var timer = 0, typedTimer = 0, typedUntil = 0, revealRaf = 0, revealTimer = 0, last = -Infinity, serialized = "";
    var active = false, baseline = true, disposed = false, listened = new AbortController();
    var tokens = new Set(), pending = [], agents = new Map(), completed = 0, newest = "", liveAbsorbed = new Set();
    var boardInput = boardLatest?.html, parsedBoard, canonicalAsks = null;
    var lastActivity = "", lastRoot = "", lastStreaming = false, failures = 0, previousPermissions = new Set();
    var lastNotifiedSessionId = "";
    var observer = new MutationObserver(queue);
    var resize = new ResizeObserver(queue);
    var activityEvents = { idle: "session.idle", thinking: "session.thinking", tool: "session.tool", replying: "session.replying", typing_user: "user.typing" };
    var terminal = new Set(["idle", "ended", "closed", "done", "failed"]);
    var watched = ["r-board", "bgal", "bcol", "drawer", "r-panels", "r-entries", "r-convlive", "r-tree", "r-head", "composer", "agisland"];
    var rightColHover = false, rightColFocus = false;
    var rightColSelector = "#bgal.bgal, #bcol.bcol, #drawer.drawer.open";
    var companionOverlaySelector = ".ws-companion-hit, .ws-companion-gear, .ws-companion-menu, .ws-companion-settings";
    var directCommit = /^(?:mcp__[^\s]+__)?(?:git_commit|git_merge|(?:ws\/)?git\.(?:commit|merge))$/i;
    var shellTool = /^(?:mcp__[^\s]+__)?(?:bash|shell(?:_command)?|exec_command|run_command|terminal)$/i;
    var concreteCommand = /(?:^|[;&|\n])\s*(?:command:\s*)?(?:[A-Z_][A-Z_0-9]*=[^\s;]+\s+)*(?:git\s+(?:commit|merge)|go\s+(?:-C\s+\S+\s+)?test|npm\s+test|pytest|cargo\s+test|(?:npx\s+)?(?:vitest|jest))(?=\s|$)/i;

    function event(kind, key) {
      if (!key) return;
      var token = kind + ":" + rid + ":" + key;
      if (tokens.has(token)) return;
      tokens.add(token);
      if (!baseline) pending.push({ kind: kind, token: token });
    }

    function askId(node) { return node.getAttribute("data-id") || node.id.replace(/^item-/, ""); }
    function asks() {
      var sent = new Set(q("#r-convlive .sentdata i[data-id]").map(el => el.dataset.id));
      if (boardInput !== parsedBoard) {
        parsedBoard = boardInput;
        if (typeof boardInput === "string") canonicalAsks = q(TL_ASK, frag(boardInput));
        else if (boardInput) {
          canonicalAsks = [].concat(boardInput.before, boardInput.order.map(key => boardInput.topics[key]), boardInput.after)
            .flatMap(node => node.nodeType === 1 ? (node.matches(TL_ASK) ? [node] : []).concat(q(TL_ASK, node)) : []);
        } else canonicalAsks = null;
      }
      var mounted = boardQ(TL_ASK);
      var ids = new Set();
      for (var el of canonicalAsks || mounted) {
        var id = askId(el);
        if (id && !sent.has(id) && !window.wbPill?.isHeld?.(el)) ids.add(id);
      }
      return { ids, mounted: mounted.filter(el => ids.has(askId(el))) };
    }

    function permissionKeys() {
      var keys = new Map();
      q("#r-convlive .permwait, #r-tree .phase.permission, #r-tree .brow.perm").forEach(el => {
        if (el.closest(".ag-source")) return;
        var agent = el.closest(".agent:not(.root), .peer");
        var id = agent?.querySelector(":scope > details > summary [data-reply-id]")?.dataset.replyId || agent?.dataset.peer || rid;
        var time = el.querySelector("time[data-ms]")?.dataset.ms;
        var tool = el.querySelector("code[title], .tool[title]")?.title;
        var args = el.querySelector(".args")?.textContent.trim();
        var detail = time || [tool, args].filter(Boolean).join(":");
        if (!keys.has(id) || (detail && keys.get(id) === id)) keys.set(id, detail ? id + ":" + detail : id);
      });
      if (doc.querySelector("#r-convlive .clive[data-phase]")?.dataset.phase === "permission" && !keys.has(rid)) keys.set(rid, rid);
      return keys;
    }

    function agentNodes() {
      var tree = region("tree");
      var hud = tree ? hudReadData(tree) : { roots: [], completed: 0 };
      var children = new Map();
      function walk(nodes) {
        for (var child of nodes) {
          var id = child.replyId || child.id;
          if (id && id !== rid) children.set(id, child);
          if (child.children) walk(child.children);
        }
      }
      walk(hud.roots);
      for (var node of hud.roots) {
        var rootId = node.replyId || node.id;
        if (rootId && node.state === "failed" && agents.get(rootId)?.state !== "failed") {
          event("session.error", "hud:" + rootId + ":" + ++failures);
        }
      }
      // A native row (the harness's own subagent) is done only on its own
      // evidence: its tree row shows it ended with a known end (knownEnd). A
      // row whose observation ended with the outcome unknown, a row that
      // left the tree (the finished-row cap evicts it), and a transition the
      // DOM skipped are never read as a finish, and never spend the
      // completed count's rise.
      var ended = new Set(), unknown = new Set(), native = new Set(), knownEnd = new Set();
      q("#r-tree .agent:not(.root)", doc).forEach(el => {
        if (el.closest(".ag-source")) return;
        var details = el.querySelector(":scope > details[data-key]"), key = details?.dataset.key;
        if (!key) return;
        var id = key.replace(/^agent-/, "");
        var reply = details.querySelector(":scope > summary [data-reply-id]")?.dataset.replyId;
        var label = details.querySelector(":scope > summary .aname")?.title;
        var endUnknown = !!(el.hasAttribute && el.hasAttribute("data-end-unknown"));
        if (endUnknown) unknown.add(reply || id);
        if (el.classList.contains("native")) {
          native.add(reply || id);
          if (!endUnknown && !el.classList.contains("running") && (el.closest(".tgroup.ended") || el.matches(".done, .failed"))) knownEnd.add(reply || id);
        }
        if (!children.has(reply || id) && !children.has(id) && !el.classList.contains("running")) {
          ended.add(reply || id);
          return;
        }
        if (el.closest(".tgroup.ended") || el.matches(".ended, .done, .failed, .closed, .idle")) ended.add(reply || id);
        if (!children.has(reply || id) && !children.has(id)) {
          children.set(reply || id, { id, replyId: reply, label, state: "thinking" });
        }
      });
      var remaining = Math.max(0, hud.completed - completed), running = 0, legacyCount = 0;
      completed = hud.completed;
      var present = new Set();
      children.forEach((node, id) => {
        var previous = agents.get(id) || agents.get(node.id);
        var alive = !terminal.has(node.state) && !ended.has(id);
        var childActive = alive && node.state !== "holding";
        var canonical = previous?.canonical || id;
        var info = previous || { canonical, spawned: baseline, reportedCurrentTurn: false, awaitingReport: false, lastDoneMs: 0, turnStartedMs: 0, doneSeq: 0 };
        var wasActive = info.active, wasAlive = info.alive;
        Object.assign(info, node, { alive, active: childActive, canonical, native: !!(info.native || node.native || native.has(id) || native.has(node.id)) });
        agents.set(id, info);
        if (node.id && id !== node.id) agents.set(node.id, info);
        present.add(canonical);
        if (!terminal.has(node.state)) legacyCount++;
        if (childActive) {
          running++;
          if (!wasActive && !baseline) {
            info.reportedCurrentTurn = false;
            info.awaitingPriorTurn = info.awaitingReport;
            // HUD time is shifted by accumulated work for the displayed timer.
            info.turnStartedMs = node.activeSince > 0 ? node.activeSince + (node.elapsedMs || 0) : Date.now();
          }
          if (!info.spawned) { event("subagent.spawn", canonical); info.spawned = true; }
        } else if (wasAlive && !alive) {
          if (info.native && !knownEnd.has(id) && !knownEnd.has(node.id)) info.awaitingReport = false;
          else if (unknown.has(id) || unknown.has(node.id)) info.awaitingReport = false;
          else {
            // A native finish is in hud.completed too: it is not left over
            // for another row's disappearance.
            if (info.native && remaining > 0) remaining--;
            if ((wasActive && !info.reportedCurrentTurn) || info.awaitingReport) done(info);
          }
        } else if (wasActive && alive) {
          info.awaitingReport = !info.reportedCurrentTurn;
        }
      });
      var disappeared = new Set();
      agents.forEach(info => {
        if (present.has(info.canonical) || disappeared.has(info.canonical) || !info.alive) return;
        if (info.native && (knownEnd.has(info.canonical) || knownEnd.has(info.id))) {
          if (remaining > 0) remaining--;
          if ((info.active && !info.reportedCurrentTurn) || info.awaitingReport) done(info);
          info.alive = info.active = false;
          disappeared.add(info.canonical);
          return;
        }
        if (info.native || unknown.has(info.canonical) || unknown.has(info.id)) {
          info.alive = info.active = info.awaitingReport = false;
          disappeared.add(info.canonical);
          return;
        }
        if (ended.has(info.canonical) || ended.has(info.id) || remaining > 0) {
          if (remaining > 0) remaining--;
          if ((info.active && !info.reportedCurrentTurn) || info.awaitingReport) done(info);
          info.alive = info.active = false;
          disappeared.add(info.canonical);
        }
      });
      return { running, legacyCount };
    }

    function done(info, rowMs, priorTurn) {
      event("subagent.done", info.canonical + ":" + ++info.doneSeq);
      info.lastDoneMs = rowMs || Date.now();
      info.awaitingPriorTurn = false;
      if (!priorTurn) info.reportedCurrentTurn = true;
      if (!priorTurn || info.active || info.reportedCurrentTurn) info.awaitingReport = false;
    }

    function reportDone(info, rowMs) {
      if (rowMs && rowMs < info.lastDoneMs) return;
      if (rowMs && rowMs < info.turnStartedMs) {
        if (info.awaitingPriorTurn && rowMs > info.lastDoneMs) done(info, rowMs, true);
        return;
      }
      // Without a time, a report during a resumed turn could belong to either turn.
      if (!rowMs && info.awaitingPriorTurn) return;
      if (info.active ? !info.reportedCurrentTurn : info.awaitingReport) done(info, rowMs);
    }

    function reportIdentity(row) {
      var source = row.dataset.childId || row.querySelector(".mpeer")?.textContent.trim();
      if (!source) return "";
      var same = agents.get(source);
      if (same) return same.canonical;
      var matches = new Set();
      agents.forEach(info => { if (info.label === source) matches.add(info.canonical); });
      // Old wake entries carry a label. A shared label cannot select a child.
      return matches.size > 1 ? "" : matches.values().next().value || source;
    }

    function celebrate(row) {
      if (row.hasAttribute("data-turn-failed") || row.querySelector(".brow.fail, .au-row.au-fail")) return false;
      if (q("span.tool[title], .au-tool[title]", row).some(tool => directCommit.test(tool.title))) return true;
      // Ignore separators inside a quoted argument, such as a test command
      // mentioned by an echo. Keep the unquoted command preview grammar.
      function passed(text) {
        return concreteCommand.test(text.replace(/"(?:\\.|[^"\\])*"|'[^']*'/g, " argument "));
      }
      if (q(".ba[title]", row).some(args => {
        var tool = args.closest(".bp")?.querySelector("span.tool");
        return shellTool.test(tool?.title || tool?.firstChild?.textContent.trim() || "") && passed(args.title);
      })) return true;
      if (q(".au-args[title]", row).some(args => {
        var tool = args.closest(".au-row")?.querySelector(".au-tool");
        return shellTool.test(tool?.title || tool?.textContent.trim() || "") && passed(args.title);
      })) return true;
      return q(".bp[title]", row).some(batch => batch.title.split("\n").some(line => {
        var match = /^(\S+)\s*(.*)$/.exec(line.trim());
        return match && (directCommit.test(match[1]) || shellTool.test(match[1]) && passed(match[2]));
      }));
    }

    function archived() {
      var rows = entriesBox()?.children || q("#convo [data-entry]");
      var top = newest;
      for (var row of rows) {
        var key = row.dataset.entry;
        if (!key) continue;
        if (row.hasAttribute("data-turn-live-absorbed")) {
          liveAbsorbed.add(key);
          continue;
        }
        var finalized = liveAbsorbed.delete(key);
        // Entries are named in ring order. Older pagination prepends them
        // to this same box and must not turn into new mail or celebrations.
        if (key <= newest && !finalized) continue;
        if (key > top) top = key;
        if (row.matches(".mailline.recv.agent-note:not(.fail), .mailline.recv.child-report:not(.fail)")) {
          if (!baseline && row.dataset.wakeCause !== "background_exit") {
            var id = reportIdentity(row), rowMs = Number(row.querySelector("time[data-ms]")?.dataset.ms) || 0;
            var info = agents.get(id);
            if (id && !info) {
              info = { canonical: id, alive: false, active: false, spawned: true, reportedCurrentTurn: false, awaitingReport: false, lastDoneMs: 0, turnStartedMs: 0, doneSeq: 0 };
              agents.set(id, info);
              done(info, rowMs);
            } else if (info) reportDone(info, rowMs);
          }
        } else if (row.matches(".mailline.recv:not(.agent-note):not(.child-report):not(.agent-card), .mail-notification:not([data-board-nudge]):not([data-mail-wake])") || row.matches("article.mail-block") && row.querySelector(":scope > details.mailline.recv")) {
          event("mail.recv", key);
        }
        if (row.matches(".mailline.sent") || row.matches("article.mail-block") && row.querySelector(":scope > details.mailline.sent")) event("mail.send", key);
        if (row.matches(".sysline.compaction:not(.fail)")) event("session.compacted", key);
        if (row.matches(".sysline.notice.fail, .sysline.compaction.fail, .mailline.recv.agent-note.fail") ||
            row.matches(".toolsline") && (row.hasAttribute("data-turn-failed") || row.querySelector(".brow.fail, .au-row.au-fail"))) event("session.error", key);
        if (row.matches(".toolsline:not(.live)") && celebrate(row)) event("session.celebrate", key);
      }
      newest = top;
    }

    function activity(wait, legacyRunning) {
      var phase = doc.querySelector("#r-convlive .clive[data-phase]")?.dataset.phase;
      var failed = doc.querySelector("#r-head .tph-chips .chip.fail, #r-tree .agent.root.failed") ||
        phase === "failed" || phase === "error";
      if (failed && !lastRoot) event("session.error", "lead:" + ++failures);
      lastRoot = failed ? "failed" : "";
      var streamingNow = !wait && phase !== "permission" && !failed && (phase === "replying" || phase === "tool" || legacyRunning > 0);
      if (!baseline && !lastStreaming && streamingNow) pending.push({ kind: "session.streaming", token: "session.streaming:" + rid + ":" + Math.round(performance.now()) });
      lastStreaming = streamingNow;
      if (wait || phase === "permission") return "permission";
      if (failed) return "error";
      if ((!phase || phase === "idle" || phase === "thinking") && performance.now() < typedUntil) return "typing_user";
      if (phase === "thinking") return "thinking";
      if (phase === "tool") return "tool";
      if (phase === "replying") return "replying";
      return "idle";
    }

    function rect(el) {
      var r = el?.getBoundingClientRect();
      if (!r?.width || !r.height || el.closest("[hidden], [inert]") ||
          r.bottom <= 0 || r.right <= 0 || r.top >= innerHeight || r.left >= innerWidth) return null;
      return { x: Math.round(r.left), y: Math.round(r.top), width: Math.round(r.width), height: Math.round(r.height) };
    }

    function visibleRect(el) {
      var r = rect(el);
      if (!r) return null;
      if (typeof getComputedStyle === "function" && getComputedStyle(el)?.visibility === "hidden") return null;
      return r;
    }

    function collect() {
      var route = rid ? "session" : doc.body.classList.contains("tk-paged") ? "tracker" : "home";
      var payload = window.WsShell.companionContext(route);
      if (rid) {
        // Cached and reconnecting pages can still have pending region swaps.
        // Read their current context silently until the first render is applied.
        var open = asks(), permissions = permissionKeys(), children = agentNodes();
        open.ids.forEach(id => {
          var token = "ask:" + rid + ":" + id;
          if (!tokens.has(token)) { tokens.add(token); if (!baseline) pending.push({ kind: "ask.open", token, rid }); }
        });
        permissions.forEach((key, owner) => {
          if (!baseline && !previousPermissions.has(owner)) pending.push({ kind: "ask.open", token: "perm:" + owner, rid: owner });
        });
        previousPermissions = new Set(permissions.keys());
        archived();
        var count = Number(doc.querySelector("#r-convlive .clive")?.dataset.subdeep) || 0;
        var act = activity(permissions.size > 0, Math.max(children.legacyCount, count));
        var kind = activityEvents[act];
        if (!baseline && kind && act !== lastActivity) pending.push({ kind, token: kind + ":" + rid + ":" + Math.round(performance.now()) });
        lastActivity = act;
        payload.sessionId = rid;
        payload.harness = lead.st?.backend || doc.querySelector("#r-head .tph-hs [data-h]")?.dataset.h || "";
        payload.model = lead.st?.model || doc.querySelector("#r-head [data-companion-model]")?.dataset.companionModel || "";
        payload.activity = act;
        payload.openAskCount = open.ids.size + permissions.size;
        payload.runningSubagents = children.running;
        var composer = rect($("composer"));
        var ask = open.mounted.map(rect).find(Boolean);
        var hud = rect($("agisland"));
        if (composer) payload.landmarks["composer-top"] = composer;
        if (ask) payload.landmarks["whiteboard-ask"] = ask;
        if (hud) payload.landmarks["hud-roster"] = hud;
      }
      var drawerEl = $("drawer");
      var drawerRect = drawerEl?.classList?.contains?.("open") ? visibleRect(drawerEl) : null;
      var rightCol = visibleRect($("bgal")) || visibleRect($("bcol")) || drawerRect;
      if (rightCol) payload.landmarks["right-col"] = rightCol;
      payload.drawerOpen = !!drawerRect;
      payload.rightColActive = !!(rightColHover || rightColFocus);
      return payload;
    }

    function notifyParentSessionSwitch(payload) {
      if (!active || disposed || pageHidden() || !rid || rid === lastNotifiedSessionId) return;
      lastNotifiedSessionId = rid;
      try {
        var parentWin = window.parent;
        if (parentWin && parentWin !== window && typeof parentWin.WsCompanion?.onSessionSwitch === "function") {
          parentWin.WsCompanion.onSessionSwitch(rid, {
            harness: payload?.harness || "",
            model: payload?.model || "",
            source: "relay"
          });
        }
      } catch (_) { /* Cross-origin or detached parent. */ }
    }

    function publish() {
      timer = 0;
      if (!active) return;
      last = performance.now();
      var payload = collect(), next = JSON.stringify(payload);
      notifyParentSessionSwitch(payload);
      payload.events = pending.splice(0);
      if (next !== serialized || payload.events.length) shPost("companion-context", payload);
      serialized = next;
    }

    function queue() {
      if (active && !timer) timer = setTimeout(publish, Math.max(0, 250 - (performance.now() - last)));
    }

    function input(event) {
      if (!event.target.matches?.("#composer textarea")) return;
      typedUntil = performance.now() + 2500;
      clearTimeout(typedTimer);
      typedTimer = setTimeout(queue, 2500);
      queue();
    }

    function clearReveal() {
      if (revealRaf && typeof cancelAnimationFrame === "function") cancelAnimationFrame(revealRaf);
      if (revealTimer) clearTimeout(revealTimer);
      revealRaf = 0;
      revealTimer = 0;
    }

    function scheduleReveal() {
      clearReveal();
      var run = function () {
        revealRaf = 0;
        revealTimer = 0;
        if (disposed || !active || pageHidden()) return;
        reconcile(true);
      };
      if (typeof requestAnimationFrame === "function") revealRaf = requestAnimationFrame(run);
      else revealTimer = setTimeout(run, 0);
    }

    function reconcile(ready) {
      if (disposed) return;
      clearReveal();
      if (!ready) {
        baseline = true;
        pending.length = 0;
        queue();
        return;
      }
      if (!active || !baseline) return;
      // Complete the baseline from the reconciled render before observing
      // subsequent DOM mutations. Drop pre-reconciliation transient UI.
      tokens.clear();
      agents.clear();
      completed = 0;
      newest = "";
      lastRoot = lastActivity = "";
      lastStreaming = false;
      previousPermissions.clear();
      collect();
      baseline = false;
      queue();
    }

    function visible() {
      if (disposed) return;
      if (pageHidden()) {
        active = false;
        lastNotifiedSessionId = "";
        observer.disconnect();
        resize.disconnect();
        clearReveal();
        clearTimeout(timer);
        clearTimeout(typedTimer);
        timer = typedTimer = typedUntil = 0;
        return;
      }
      if (active) return;
      active = true;
      baseline = true;
      showRegions();
      observer.observe(doc.body, { attributes: true, attributeFilter: ["class"] });
      for (var id of watched) {
        var el = $(id);
        if (!el) continue;
        observer.observe(el, { subtree: true, childList: true, attributes: true, characterData: true,
          attributeFilter: ["class", "hidden", "data-phase", "data-assumed", "open", "data-companion-model"] });
        resize.observe(el);
      }
      if (!rid || companionStreamReady) scheduleReveal();
      else queue();
    }

    function inRightCol(target) {
      return !!(target && typeof target.closest === "function" && target.closest(rightColSelector));
    }

    function inCompanionOverlay(target) {
      return !!(target && typeof target.closest === "function" && target.closest(companionOverlaySelector));
    }

    function parentPointerInCompanion(e) {
      try {
        var parentWin = window.parent;
        if (!parentWin || parentWin === window) return false;
        var parentDoc = parentWin.document;
        if (!parentDoc) return false;
        if (parentDoc.querySelector?.('.ws-companion[data-hover="1"]')) return true;
        var frameEl = window.frameElement;
        if (frameEl && typeof parentDoc.elementFromPoint === "function" &&
            Number.isFinite(e?.clientX) && Number.isFinite(e?.clientY)) {
          var r = frameEl.getBoundingClientRect();
          var hit = parentDoc.elementFromPoint(r.left + e.clientX, r.top + e.clientY);
          return inCompanionOverlay(hit);
        }
      } catch (_) { /* Cross-origin or detached parent. */ }
      return false;
    }

    function setRightColHover(next) {
      if (rightColHover === next) return;
      rightColHover = next;
      queue();
    }

    function setRightColFocus(next) {
      if (rightColFocus === next) return;
      rightColFocus = next;
      queue();
    }

    function handleRightColPointer(e) {
      if (inCompanionOverlay(e?.target)) return;
      setRightColHover(inRightCol(e?.target));
    }

    var listen = { signal: listened.signal };
    doc.addEventListener("visibilitychange", visible, listen);
    doc.addEventListener("ws-page-visibility", visible, listen);
    doc.addEventListener("input", input, listen);
    doc.addEventListener("change", queue, listen);
    doc.addEventListener("scroll", queue, { ...listen, capture: true });
    doc.addEventListener("pointerover", handleRightColPointer, { ...listen, passive: true });
    doc.addEventListener("pointermove", handleRightColPointer, { ...listen, passive: true });
    doc.documentElement?.addEventListener?.("pointerleave", function (e) {
      if (parentPointerInCompanion(e)) return;
      setRightColHover(false);
    }, { ...listen, passive: true });
    doc.addEventListener("focusin", function (e) { setRightColFocus(inRightCol(e.target)); }, listen);
    doc.addEventListener("focusout", function (e) { setRightColFocus(inRightCol(e.relatedTarget)); }, listen);
    window.addEventListener("resize", queue, listen);
    visible();
    return {
      reconcile,
      board(input) {
        // The rendered board can intentionally hold a new topic in place.
        // Count asks from the reconciled stream and use DOM only for geometry.
        boardInput = input;
        queue();
      },
      dispose() {
        disposed = true;
        listened.abort();
        observer.disconnect();
        resize.disconnect();
        clearReveal();
        clearTimeout(timer);
        clearTimeout(typedTimer);
        active = false;
      }
    };
  }
