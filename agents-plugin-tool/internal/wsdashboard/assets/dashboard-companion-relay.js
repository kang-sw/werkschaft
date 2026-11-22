  // A framed view creates its observer only while the outer shell opts in.
  function startCompanionRelay() {
    var timer = 0, typedTimer = 0, typedUntil = 0, last = -Infinity, serialized = "";
    var active = false, baseline = true, listened = new AbortController();
    var tokens = new Set(), pending = [], agents = new Map(), completed = 0, newest = "";
    var boardInput = boardLatest?.html, parsedBoard, canonicalAsks = null;
    var lastActivity = "", lastRoot = "", failures = 0;
    var observer = new MutationObserver(queue);
    var resize = new ResizeObserver(queue);
    var activityEvents = { idle: "session.idle", thinking: "session.thinking", streaming: "session.streaming", typing_user: "user.typing" };
    var terminal = new Set(["idle", "ended", "closed", "done", "failed"]);
    var watched = ["r-board", "bgal", "r-panels", "r-entries", "r-convlive", "r-tree", "r-head", "composer", "agisland"];
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
        var id = agent?.querySelector(":scope > details[data-key]")?.dataset.key || agent?.dataset.peer || "lead";
        var time = el.querySelector("time[data-ms]")?.dataset.ms;
        var tool = el.querySelector("code[title], .tool[title]")?.title;
        var args = el.querySelector(".args")?.textContent.trim();
        var detail = time || [tool, args].filter(Boolean).join(":");
        if (!keys.has(id) || (detail && keys.get(id) === id)) keys.set(id, detail ? id + ":" + detail : id);
      });
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
      var ended = new Set();
      q("#r-tree .agent:not(.root)", doc).forEach(el => {
        if (el.closest(".ag-source")) return;
        var details = el.querySelector(":scope > details[data-key]"), key = details?.dataset.key;
        if (!key) return;
        var id = key.replace(/^agent-/, "");
        var reply = details.querySelector(":scope > summary [data-reply-id]")?.dataset.replyId;
        var label = details.querySelector(":scope > summary .aname")?.title;
        if (!children.has(reply || id) && !children.has(id) && !el.classList.contains("running")) {
          ended.add(reply || id);
          return;
        }
        if (el.closest(".tgroup.ended") || el.matches(".ended, .done, .failed, .closed, .idle")) ended.add(reply || id);
        if (!children.has(reply || id) && !children.has(id)) {
          children.set(reply || id, { id, replyId: reply, label, state: "thinking" });
        }
      });
      var remaining = Math.max(0, hud.completed - completed), running = 0;
      completed = hud.completed;
      var present = new Set();
      children.forEach((node, id) => {
        var previous = agents.get(id) || agents.get(node.id);
        var live = !terminal.has(node.state) && !ended.has(id);
        var canonical = previous?.canonical || id;
        var info = { ...node, live, canonical };
        agents.set(id, info);
        if (node.id && id !== node.id) agents.set(node.id, info);
        present.add(canonical);
        if (live) {
          running++;
          if (!previous) event("subagent.spawn", canonical);
        } else if (previous?.live) event("subagent.done", canonical);
      });
      var disappeared = new Set();
      agents.forEach(info => {
        if (present.has(info.canonical) || disappeared.has(info.canonical) || !info.live) return;
        if (ended.has(info.canonical) || ended.has(info.id) || remaining > 0) {
          if (remaining > 0) remaining--;
          info.live = false;
          disappeared.add(info.canonical);
          event("subagent.done", info.canonical);
        }
      });
      return running;
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
      if (row.querySelector(".brow.fail")) return false;
      if (q("span.tool[title]", row).some(tool => directCommit.test(tool.title))) return true;
      // Ignore separators inside a quoted argument, such as a test command
      // mentioned by an echo. Keep the unquoted command preview grammar.
      function passed(text) {
        return concreteCommand.test(text.replace(/"(?:\\.|[^"\\])*"|'[^']*'/g, " argument "));
      }
      if (q(".ba[title]", row).some(args => {
        var tool = args.closest(".bp")?.querySelector("span.tool");
        return shellTool.test(tool?.title || tool?.firstChild?.textContent.trim() || "") && passed(args.title);
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
        // Entries are named in ring order. Older pagination prepends them
        // to this same box and must not turn into new mail or celebrations.
        if (!key || key <= newest) continue;
        if (key > top) top = key;
        if (row.matches(".mailline.recv.agent-note:not(.fail), .mailline.recv.child-report:not(.fail)")) {
          if (row.dataset.wakeCause !== "background_exit") event("subagent.done", reportIdentity(row));
        } else if (row.matches(".mailline.recv, .mail-notification") || row.matches(".mail-block") && row.querySelector(".mailline.recv")) {
          if (!row.matches(".agent-note, .child-report")) event("mail.recv", key);
        }
        if (row.matches(".sysline.notice.fail, .sysline.compaction.fail, .mailline.recv.agent-note.fail") ||
            row.matches(".toolsline") && row.querySelector(".brow.fail")) event("session.error", key);
        if (row.matches(".toolsline:not(.live)") && celebrate(row)) event("session.celebrate", key);
      }
      newest = top;
    }

    function activity(wait, running) {
      var phase = doc.querySelector("#r-convlive .clive[data-phase]")?.dataset.phase;
      var failed = doc.querySelector("#r-head .tph-chips .chip.fail, #r-tree .agent.root.failed") ||
        phase === "failed" || phase === "error";
      if (failed && !lastRoot) event("session.error", "lead:" + ++failures);
      lastRoot = failed ? "failed" : "";
      if (wait || phase === "permission") return "permission";
      if (failed) return "error";
      if (phase === "replying" || phase === "tool" || running > 0) return "streaming";
      if (phase === "thinking") return "thinking";
      if (performance.now() < typedUntil) return "typing_user";
      return "idle";
    }

    function rect(el) {
      var r = el?.getBoundingClientRect();
      if (!r?.width || !r.height || el.closest("[hidden], [inert]") ||
          r.bottom <= 0 || r.right <= 0 || r.top >= innerHeight || r.left >= innerWidth) return null;
      return { x: Math.round(r.left), y: Math.round(r.top), width: Math.round(r.width), height: Math.round(r.height) };
    }

    function collect() {
      var route = rid ? "session" : doc.body.classList.contains("tk-paged") ? "tracker" : "home";
      var payload = window.WsShell.companionContext(route);
      if (rid) {
        // Cached and reconnecting pages can still have pending region swaps.
        // Read their current context silently until the first render is applied.
        var open = asks(), permissions = permissionKeys(), running = agentNodes();
        open.ids.forEach(id => event("ask.open", id));
        permissions.forEach(key => event("ask.open", "permission:" + key));
        archived();
        var count = Number(doc.querySelector("#r-convlive .clive")?.dataset.subdeep) || 0;
        var act = activity(permissions.size > 0, Math.max(running, count));
        var kind = activityEvents[act];
        if (!baseline && kind && act !== lastActivity) pending.push({ kind, token: kind + ":" + rid + ":" + Math.round(performance.now()) });
        lastActivity = act;
        payload.sessionId = rid;
        payload.harness = lead.st?.backend || doc.querySelector("#r-head .tph-hs [data-h]")?.dataset.h || "";
        payload.model = lead.st?.model || doc.querySelector("#r-head [data-companion-model]")?.dataset.companionModel || "";
        payload.activity = act;
        payload.openAskCount = open.ids.size + permissions.size;
        payload.runningSubagents = Math.max(running, count);
        var composer = rect($("composer"));
        var ask = open.mounted.map(rect).find(Boolean);
        var hud = rect($("agisland"));
        if (composer) payload.landmarks["composer-top"] = composer;
        if (ask) payload.landmarks["whiteboard-ask"] = ask;
        if (hud) payload.landmarks["hud-roster"] = hud;
      }
      return payload;
    }

    function publish() {
      timer = 0;
      if (!active) return;
      last = performance.now();
      var payload = collect(), next = JSON.stringify(payload);
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

    function reconcile(ready) {
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
      collect();
      baseline = false;
      queue();
    }

    function visible() {
      if (pageHidden()) {
        active = false;
        observer.disconnect();
        resize.disconnect();
        clearTimeout(timer);
        clearTimeout(typedTimer);
        timer = typedTimer = typedUntil = 0;
        return;
      }
      if (active) return;
      active = true;
      baseline = true;
      observer.observe(doc.body, { attributes: true, attributeFilter: ["class"] });
      for (var id of watched) {
        var el = $(id);
        if (!el) continue;
        observer.observe(el, { subtree: true, childList: true, attributes: true, characterData: true,
          attributeFilter: ["class", "hidden", "data-phase", "data-assumed", "open", "data-companion-model"] });
        resize.observe(el);
      }
      if (!rid || companionStreamReady) reconcile(true);
      else queue();
    }

    var listen = { signal: listened.signal };
    doc.addEventListener("visibilitychange", visible, listen);
    doc.addEventListener("ws-page-visibility", visible, listen);
    doc.addEventListener("input", input, listen);
    doc.addEventListener("change", queue, listen);
    doc.addEventListener("scroll", queue, { ...listen, capture: true });
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
        listened.abort();
        observer.disconnect();
        resize.disconnect();
        clearTimeout(timer);
        clearTimeout(typedTimer);
        active = false;
      }
    };
  }
