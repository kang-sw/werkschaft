// Background agent ws/exec monitor popover (Variant C: compact breadcrumb list
// + docked bottom live-tail drawer). Anchored to #bgbadge, strictly separate
// from the user-spawned `!` exec HUD (.xgroup / /exec/jobs). Zero network
// requests and zero timers while closed.
  var BG_TAIL_BYTES = 16384, BG_MAX_LINES = 800, BG_POLL_MIN = 500, BG_POLL_MAX = 2500;
  var bgSt = {
    open: false,
    loading: false,
    error: false,
    canControl: false,
    selfRunning: 0,
    descRunning: 0,
    totalRunning: 0,
    totalFinished: 0,
    jobs: [],
    byId: Object.create(null),
    selId: "",
    stopping: Object.create(null),
    follow: true,
    rev: -1
  };
  var bgPop = null, bgListEl = null, bgDockEl = null, bgTailEl = null, bgTailFootEl = null;
  var bgRows = new Map(), bgListDivider = null, bgListEmpty = null;
  var bgDockJob = "", bgDockHead = null, bgDockMeta = null, bgDockMetaKey = "";
  var bgSplitEl = null, bgCountsEl = null, bgStopBtn = null, bgStopHold = null;
  var bgListRequest = null, bgListDirty = false;
  var bgTailState = null, bgTailTimer = 0, bgTailGen = 0, bgTailAC = null;
  var bgTickTimer = 0, bgCopyTimer = 0;

  function bgExecOpen() { return bgSt.open; }
  function bgPageHidden() { return typeof pageHidden === "function" ? pageHidden() : !!doc.hidden; }

  function bgEl(tag, cls, text) {
    var e = doc.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  }

  function bgFmtElapsed(ms) {
    var s = Math.max(0, Math.floor(ms / 1000));
    if (s < 60) return s + "s";
    var h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), r = s % 60;
    function p(n) { return n < 10 ? "0" + n : "" + n; }
    return h ? h + ":" + p(m) + ":" + p(r) : p(m) + ":" + p(r);
  }

  function bgFmtBytes(n) {
    n = Math.max(0, Number(n) || 0);
    if (n < 1024) return n + " B";
    if (n < 1048576) return (n / 1024).toFixed(1) + " KiB";
    return (n / 1048576).toFixed(2) + " MiB";
  }

  function bgShortCwd(cwd) {
    if (!cwd) return "";
    var parts = String(cwd).replace(/\\/g, "/").replace(/\/+$/, "").split("/").filter(Boolean);
    if (parts.length <= 2) return parts.join("/");
    return "\u2026/" + parts.slice(-2).join("/");
  }

  function bgJobState(j) {
    if (!j) return "stopped";
    if (j.state === "running") return bgSt.stopping[j.id] ? "stopping" : "running";
    if (j.state === "exited") return j.exit_code === 0 && j.signal == null ? "ok" : "failed";
    if (j.state === "failed") return "nostart";
    return j.state === "stopped" || j.state === "lost" ? j.state : "failed";
  }

  function bgDotClass(j) {
    var s = bgJobState(j);
    if (s === "running") return "is-run";
    if (s === "ok") return "is-ok";
    if (s === "failed") return "is-fail";
    if (s === "lost" || s === "nostart") return "is-warn";
    return "is-stop";
  }

  function bgStateText(j) {
    var s = bgJobState(j);
    if (s === "failed") {
      return j.signal != null ? wsT("js.xhud.st.sig", String(j.signal)) : wsT("js.xhud.st.failed", String(j.exit_code == null ? "?" : j.exit_code));
    }
    if (s === "stopped") return j.stopped_by_user ? wsT("js.xhud.st.stopped.user") : wsT("js.xhud.st.stopped");
    var map = {
      running: "js.xhud.st.running",
      stopping: "js.bgexec.stopping",
      ok: "js.xhud.st.ok",
      lost: "js.xhud.st.lost",
      nostart: "js.xhud.st.nostart"
    };
    return wsT(map[s] || "js.xhud.st.stopped");
  }

  function bgOwnerParts(j) {
    var depth = typeof j.owner_depth === "number" ? j.owner_depth : 0;
    var path = Array.isArray(j.owner_path) && j.owner_path.length ? j.owner_path : (j.owner_label ? [j.owner_label] : []);
    if (depth <= 0) {
      return { depth: 0, badge: wsT("js.bgexec.self"), crumb: "", full: wsT("js.bgexec.self") };
    }
    var full = path.join(" \u203a ") || j.owner_id || ("L" + depth);
    var crumb = path.length <= 2 ? full : (path[path.length - 2] + " \u203a " + path[path.length - 1]);
    return { depth: depth, badge: "L" + depth, crumb: crumb, full: "L" + depth + " \u00b7 " + full };
  }

  function bgEnsurePop() {
    if (bgPop) return bgPop;
    bgPop = bgEl("div", "bgpop");
    bgPop.id = "bgpop";
    bgPop.hidden = true;
    bgPop.setAttribute("role", "dialog");
    bgPop.setAttribute("aria-modal", "false");
    bgPop.setAttribute("aria-label", wsT("js.bgexec.title"));

    var hd = bgEl("div", "bgp-hd");
    hd.appendChild(bgEl("span", "bgp-title", wsT("js.bgexec.title")));
    bgSplitEl = bgEl("span", "bgp-split");
    bgSplitEl.hidden = true;
    hd.appendChild(bgSplitEl);
    hd.appendChild(bgEl("span", "bgp-spacer"));
    bgCountsEl = bgEl("span", "bgp-counts");
    hd.appendChild(bgCountsEl);
    var closeBtn = bgEl("button", "bgp-ibtn", "\u00d7");
    closeBtn.type = "button";
    closeBtn.title = wsT("js.bgexec.close");
    closeBtn.setAttribute("aria-label", wsT("js.bgexec.close"));
    closeBtn.addEventListener("click", function () { bgExecClose(true); });
    hd.appendChild(closeBtn);
    bgPop.appendChild(hd);

    bgListEl = bgEl("div", "bgp-list");
    bgListEl.setAttribute("role", "listbox");
    bgListEl.setAttribute("aria-label", wsT("js.bgexec.title"));
    bgListEl.addEventListener("click", function (e) {
      var row = e.target && e.target.closest ? e.target.closest(".bgp-row[data-bg-id]") : null;
      if (!row) return;
      var id = row.getAttribute("data-bg-id");
      if (id) bgSelectJob(id, false);
    });
    bgPop.appendChild(bgListEl);

    bgDockEl = bgEl("div", "bgp-dock");
    bgDockEl.hidden = true;
    bgPop.appendChild(bgDockEl);

    doc.body.appendChild(bgPop);

    doc.addEventListener("pointerdown", function (e) {
      if (!bgSt.open) return;
      if (!e.target || !e.target.isConnected) return;
      if (bgPop.contains(e.target) || (bgbadge && bgbadge.contains(e.target))) return;
      bgExecClose(false);
    }, true);

    window.addEventListener("keydown", function (e) {
      if (!bgSt.open) return;
      if (e.key === "Escape") {
        if (window.WsHoldConfirm && window.WsHoldConfirm.isHolding()) return;
        e.preventDefault();
        e.stopPropagation();
        bgExecClose(true);
        return;
      }
      if ((e.key === "ArrowDown" || e.key === "ArrowUp") && bgSt.jobs.length > 1) {
        var active = doc.activeElement;
        if (!active || (!bgPop.contains(active) && active !== bgbadge)) return;
        e.preventDefault();
        var idx = bgSt.jobs.findIndex(function (j) { return j.id === bgSt.selId; });
        var delta = e.key === "ArrowDown" ? 1 : -1;
        var next = bgSt.jobs[(idx < 0 ? 0 : idx + delta + bgSt.jobs.length) % bgSt.jobs.length];
        if (next) bgSelectJob(next.id, true);
      }
    }, true);

    window.addEventListener("resize", function () {
      if (bgSt.open) bgPosition();
    });

    function bgOnVisibility() {
      if (!bgSt.open) return;
      if (bgPageHidden()) {
        if (bgListRequest) { bgListDirty = true; bgAbortList(); }
        bgStopTick();
        clearTimeout(bgTailTimer);
        bgTailTimer = 0;
        if (bgTailAC) { try { bgTailAC.abort(); } catch (e) {} bgTailAC = null; }
        if (bgTailState) bgTailState.busy = false;
      } else {
        if (bgListDirty) bgExecFetchList();
        bgSyncTick();
        if (bgTailState && !bgTailState.drained && !bgTailState.busy) bgTailSchedule(0);
      }
    }
    doc.addEventListener("visibilitychange", bgOnVisibility);
    doc.addEventListener("ws-page-visibility", bgOnVisibility);

    return bgPop;
  }

  function bgPosition() {
    if (!bgPop || !bgSt.open || !bgbadge) return;
    var vw = window.innerWidth || doc.documentElement.clientWidth || 1024;
    var vh = window.innerHeight || doc.documentElement.clientHeight || 768;
    var margin = 8;
    var popW = Math.min(500, Math.max(260, vw - margin * 2));
    bgPop.style.width = popW + "px";
    var rect = bgbadge.getBoundingClientRect();
    var left = rect.right - popW;
    left = Math.max(margin, Math.min(left, vw - popW - margin));
    var availAbove = Math.max(220, rect.top - 18);
    var maxH = Math.min(520, availAbove);
    var bottom = Math.max(margin, vh - rect.top + 10);
    bgPop.style.left = Math.round(left) + "px";
    bgPop.style.bottom = Math.round(bottom) + "px";
    bgPop.style.maxHeight = Math.round(maxH) + "px";
  }

  function bgExecToggle() {
    if (bgSt.open) bgExecClose(false);
    else bgExecOpenPop();
  }

  function bgExecOpenPop() {
    if (!rid) return;
    bgEnsurePop();
    bgSt.open = true;
    bgSt.error = false;
    bgSt.loading = bgSt.jobs.length === 0;
    bgPop.hidden = false;
    if (bgbadge) bgbadge.setAttribute("aria-expanded", "true");
    bgRenderAll();
    bgPosition();
    bgExecFetchList();
    bgSyncTick();
  }

  function bgExecClose(restoreFocus) {
    if (!bgSt.open) return;
    bgSt.open = false;
    bgStopTick();
    bgTailStop();
    bgAbortList();
    bgListDirty = false;
    bgClearStop();
    clearTimeout(bgCopyTimer);
    bgCopyTimer = 0;
    if (bgPop) bgPop.hidden = true;
    if (bgbadge) {
      bgbadge.setAttribute("aria-expanded", "false");
      if (typeof bgBadge === "function") bgBadge(doc.querySelector("#r-convlive .clive"));
      if (restoreFocus && !bgbadge.hidden) bgbadge.focus();
    }
  }

  function bgStopTick() {
    if (bgTickTimer) { clearInterval(bgTickTimer); bgTickTimer = 0; }
  }

  function bgSyncTick() {
    var hasRunning = bgSt.open && !bgPageHidden() && bgSt.jobs.some(function (j) { return j.state === "running"; });
    if (hasRunning && !bgTickTimer) {
      bgTickTimer = setInterval(function () {
        if (!bgSt.open || bgPageHidden()) { bgStopTick(); return; }
        var now = Date.now();
        if (bgPop) {
          var nodes = bgPop.querySelectorAll("[data-bg-since]");
          for (var i = 0; i < nodes.length; i++) {
            var since = Number(nodes[i].getAttribute("data-bg-since")) || 0;
            if (since) nodes[i].textContent = bgFmtElapsed(now - since);
          }
        }
      }, 1000);
    } else if (!hasRunning && bgTickTimer) {
      bgStopTick();
    }
  }

  function bgExecWake(rev) {
    if (!bgSt.open || !rid) return;
    if (typeof rev === "number" && rev === bgSt.rev) return;
    bgExecFetchList();
  }

  function bgAbortList() {
    var request = bgListRequest;
    bgListRequest = null;
    if (request && request.controller) request.controller.abort();
  }

  function bgExecFetchList() {
    if (!bgSt.open || !rid) return;
    if (bgPageHidden() || bgListRequest) { bgListDirty = true; return; }
    bgListDirty = false;
    var request = { controller: window.AbortController ? new AbortController() : null };
    bgListRequest = request;
    var opts = { credentials: "same-origin", cache: "no-store" };
    if (request.controller) opts.signal = request.controller.signal;
    fetch("/s/" + rid + "/exec/bg/jobs", opts).then(function (r) {
      if (!r.ok) throw new Error("HTTP " + r.status);
      return r.json();
    }).then(function (data) {
      if (bgListRequest !== request) return;
      bgListRequest = null;
      if (bgPageHidden()) { bgListDirty = true; return; }
      bgSt.loading = false;
      bgSt.error = false;
      bgSt.rev = typeof data.rev === "number" ? data.rev : bgSt.rev;
      bgSt.canControl = !!data.can_control;
      bgSt.selfRunning = Number(data.self_running) || 0;
      bgSt.descRunning = Number(data.descendant_running) || 0;
      bgSt.totalRunning = Number(data.total_running) || 0;
      bgSt.totalFinished = Number(data.total_finished) || 0;
      var list = Array.isArray(data.jobs) ? data.jobs.filter(function (j) { return j && j.origin === "agent"; }) : [];
      bgSt.jobs = list;
      bgSt.byId = Object.create(null);
      list.forEach(function (j) {
        bgSt.byId[j.id] = j;
        if (j.state !== "running") delete bgSt.stopping[j.id];
      });
      var prevSel = bgSt.selId;
      if (!bgSt.selId || !bgSt.byId[bgSt.selId]) {
        var firstRun = list.find(function (j) { return j.state === "running"; });
        bgSt.selId = firstRun ? firstRun.id : (list[0] ? list[0].id : "");
      }
      bgRenderAll();
      bgPosition();
      bgSyncTick();
      var sel = bgSt.byId[bgSt.selId];
      if (!sel) {
        bgTailStop();
      } else if (sel.id !== prevSel || !bgTailState || bgTailState.id !== sel.id) {
        bgTailStart(sel);
      } else if (sel.state !== "running" && bgTailState && !bgTailState.drained) {
        bgTailSchedule(0);
      } else if (sel.state === "running" && bgTailState) {
        bgTailState.delay = BG_POLL_MIN;
        bgTailSchedule(0);
      }
      if (bgListDirty && bgSt.open) bgExecFetchList();
    }, function (err) {
      if (bgListRequest !== request) return;
      bgListRequest = null;
      if (bgPageHidden()) { bgListDirty = true; return; }
      if (err && err.name === "AbortError") return;
      bgSt.loading = false;
      bgSt.error = true;
      bgRenderAll();
      bgPosition();
      if (bgListDirty) bgExecFetchList();
    });
  }

  function bgSelectJob(id, focusRow) {
    if (!bgSt.byId[id]) return;
    var changed = bgSt.selId !== id;
    bgSt.selId = id;
    if (changed) bgSt.follow = true;
    bgRenderList();
    bgRenderDock();
    bgPosition();
    if (changed || !bgTailState || bgTailState.id !== id) {
      bgTailStart(bgSt.byId[id]);
    }
    if (focusRow && bgListEl) {
      var children = bgListEl.children;
      for (var i = 0; i < children.length; i++) {
        if (children[i].getAttribute && children[i].getAttribute("data-bg-id") === id) {
          children[i].focus();
          break;
        }
      }
    }
  }

  function bgRenderAll() {
    bgRenderHeader();
    bgRenderList();
    bgRenderDock();
  }

  function bgRenderHeader() {
    if (!bgSplitEl || !bgCountsEl) return;
    var key = [bgSt.selfRunning, bgSt.descRunning, bgSt.totalRunning, bgSt.totalFinished].join("/");
    if (bgCountsEl.bgKey === key) return;
    bgCountsEl.bgKey = key;
    if (bgSt.descRunning > 0) {
      bgSplitEl.hidden = false;
      bgSplitEl.textContent = wsT("js.bgexec.split", String(bgSt.selfRunning), String(bgSt.descRunning));
    } else {
      bgSplitEl.hidden = true;
      bgSplitEl.textContent = "";
    }
    bgCountsEl.textContent = "";
    if (bgSt.totalRunning > 0 || bgSt.totalFinished === 0) {
      bgCountsEl.appendChild(bgEl("span", "bgp-cpill is-run", wsT("js.bgexec.running", String(bgSt.totalRunning))));
    }
    if (bgSt.totalFinished > 0) {
      bgCountsEl.appendChild(bgEl("span", "bgp-cpill is-done", wsT("js.bgexec.finished", String(bgSt.totalFinished))));
    }
  }

  function bgRenderList() {
    if (!bgListEl) return;
    bgRows.forEach(function (row, id) {
      if (bgSt.byId[id]) return;
      row.remove();
      bgRows.delete(id);
    });
    if (!bgSt.jobs.length) {
      if (bgListDivider) bgListDivider.remove();
      var emptyKey = bgSt.loading ? "loading" : (bgSt.error ? "error" : "empty");
      if (bgListEmpty && bgListEmpty.bgKey === emptyKey) return;
      if (bgListEmpty) bgListEmpty.remove();
      bgListEmpty = bgEl("div", "bgp-empty" + (bgSt.error ? " is-err" : ""), wsT("js.bgexec." + emptyKey));
      bgListEmpty.bgKey = emptyKey;
      bgListEl.appendChild(bgListEmpty);
      return;
    }
    if (bgListEmpty) { bgListEmpty.remove(); bgListEmpty = null; }
    var now = Date.now(), hadRun = false, hasDivider = false;
    var next = bgListEl.firstChild, focused = doc.activeElement;
    function place(node) {
      if (node !== next) bgListEl.insertBefore(node, next);
      next = node.nextSibling;
    }
    bgSt.jobs.forEach(function (j) {
      var running = j.state === "running";
      if (running) hadRun = true;
      if (!running && hadRun && !hasDivider) {
        hasDivider = true;
        if (!bgListDivider) bgListDivider = bgEl("div", "bgp-div", wsT("js.bgexec.recent"));
        place(bgListDivider);
      }
      var selected = j.id === bgSt.selId;
      var row = bgRows.get(j.id);
      if (!row) {
        row = bgEl("button", "bgp-row");
        row.type = "button";
        row.setAttribute("role", "option");
        row.setAttribute("data-bg-id", j.id);
        bgRows.set(j.id, row);
      }
      var flag = selected ? "true" : "false";
      if (row.getAttribute("aria-selected") !== flag) {
        row.setAttribute("aria-selected", flag);
        row.classList.toggle("is-selected", selected);
      }
      bgRenderRow(row, j, now);
      place(row);
    });
    if (!hasDivider && bgListDivider) bgListDivider.remove();
    if (bgListEl.contains(focused) && focused !== doc.activeElement) focused.focus({ preventScroll: true });
  }

  function bgRenderRow(row, j, now) {
    var running = j.state === "running";
    var own = bgOwnerParts(j);
    var key = JSON.stringify([j.command, j.tool, j.cwd, own.full, own.crumb, j.started_at, bgStateText(j), bgDotClass(j), j.exit_code]);
    if (row.bgKey === key) return;
    row.bgKey = key;
    row.classList.toggle("is-done", !running);
    row.textContent = "";
    row.appendChild(bgEl("span", "bgp-dot " + bgDotClass(j)));

    var main = bgEl("div", "bgp-main");
    var cmd = bgEl("span", "bgp-cmd", j.command || j.tool || j.id);
    cmd.title = j.command || j.id;
    main.appendChild(cmd);

    var sub = bgEl("span", "bgp-sub");
    var dep = bgEl("span", "bgp-depth" + (own.depth === 0 ? " is-self" : ""), own.badge);
    dep.title = own.full;
    sub.appendChild(dep);
    if (own.crumb) {
      var anc = bgEl("span", "bgp-anc", own.crumb);
      anc.title = own.full;
      sub.appendChild(anc);
    }
    if (j.cwd) {
      var cwdEl = bgEl("span", "bgp-cwd", (own.crumb ? "\u00b7 " : "") + bgShortCwd(j.cwd));
      cwdEl.title = j.cwd;
      sub.appendChild(cwdEl);
    }
    main.appendChild(sub);
    row.appendChild(main);

    var right;
    if (running) {
      right = bgEl("span", "bgp-el", j.started_at ? bgFmtElapsed(now - j.started_at) : "0s");
      if (j.started_at) right.setAttribute("data-bg-since", String(j.started_at));
    } else {
      var st = bgJobState(j);
      var txt = st === "ok" ? "0" : (st === "failed" && j.exit_code != null ? String(j.exit_code) : bgStateText(j));
      right = bgEl("span", "bgp-el is-st " + bgDotClass(j), txt);
      right.title = bgStateText(j);
    }
    row.appendChild(right);
  }

  function bgClearStop() {
    if (bgStopHold) { bgStopHold.destroy(); bgStopHold = null; }
    if (bgStopBtn) { bgStopBtn.remove(); bgStopBtn = null; }
  }

  function bgRenderDock() {
    if (!bgDockEl) return;
    var j = bgSt.byId[bgSt.selId];
    if (!j || j.id !== bgDockJob) {
      bgClearStop();
      bgDockEl.textContent = "";
      bgDockJob = j ? j.id : "";
      bgDockHead = bgDockMeta = bgTailEl = bgTailFootEl = null;
      bgDockMetaKey = "";
    }
    bgDockEl.hidden = !j;
    if (!j) return;

    var command = j.command || j.id;
    if (!bgDockHead) {
      bgDockHead = bgEl("div", "bgp-dock-hd");
      bgDockHead.appendChild(bgEl("span", "bgp-dot"));
      bgDockHead.appendChild(bgEl("span", "bgp-dock-cmd"));
      var copyBtn = bgEl("button", "bgp-act", wsT("js.bgexec.copy"));
      copyBtn.type = "button";
      copyBtn.addEventListener("click", function () {
        var current = bgSt.byId[bgDockJob];
        var text = (bgTailEl && bgTailEl.textContent && !bgTailEl.querySelector(".bgp-tail-empty")) ? bgTailEl.textContent : (current && current.command || "");
        if (navigator.clipboard && navigator.clipboard.writeText) {
          navigator.clipboard.writeText(text).then(function () {
            copyBtn.textContent = wsT("js.bgexec.copied");
            clearTimeout(bgCopyTimer);
            bgCopyTimer = setTimeout(function () { copyBtn.textContent = wsT("js.bgexec.copy"); }, 1400);
          }, function () {});
        }
      });
      bgDockHead.appendChild(copyBtn);
      bgDockEl.appendChild(bgDockHead);
    }
    var dot = bgDockHead.firstChild, dcmd = dot.nextSibling;
    var dotClass = "bgp-dot " + bgDotClass(j);
    if (dot.className !== dotClass) dot.className = dotClass;
    if (dcmd.title !== command) { dcmd.textContent = command; dcmd.title = command; }

    if (j.state === "running" && bgSt.canControl) {
      var stopping = !!bgSt.stopping[j.id];
      if (!bgStopBtn || bgStopBtn.disabled !== stopping) {
        bgClearStop();
        var sbtn = bgEl("button", "bgp-stop" + (stopping ? " is-busy" : ""));
        sbtn.type = "button";
        sbtn.disabled = stopping;
        sbtn.setAttribute("data-bg-stop", j.id);
        sbtn.appendChild(bgEl("span", "bgp-stop-sq"));
        sbtn.appendChild(bgEl("span", "bgp-stop-lbl", stopping ? wsT("js.bgexec.stopping") : wsT("js.bgexec.stop.hold")));
        bgDockHead.appendChild(sbtn);
        bgStopBtn = sbtn;
        if (!stopping && window.WsHoldConfirm && typeof window.WsHoldConfirm.attach === "function") {
          bgStopHold = window.WsHoldConfirm.attach(sbtn, {
            duration: 1500,
            mode: "bar",
            canStart: function () {
              var current = bgSt.byId[j.id];
              return bgSt.open && bgSt.selId === j.id && bgSt.canControl && !bgSt.stopping[j.id] && current && current.state === "running";
            },
            onConfirm: function () { bgStopJob(j.id); }
          });
        }
      }
      var stopLabel = wsT("js.bgexec.stop.aria", command);
      if (bgStopBtn.getAttribute("aria-label") !== stopLabel) bgStopBtn.setAttribute("aria-label", stopLabel);
    } else {
      bgClearStop();
    }

    var own = bgOwnerParts(j);
    var metaKey = JSON.stringify([own.full, j.cwd, j.state, bgSt.canControl, dotClass, bgStateText(j)]);
    if (bgDockMetaKey !== metaKey) {
      bgDockMetaKey = metaKey;
      var meta = bgEl("div", "bgp-dock-meta");
      var ownEl = bgEl("span", "bgp-meta-own", own.full);
      ownEl.title = own.full;
      meta.appendChild(ownEl);
      if (j.cwd) {
        var cwdCode = bgEl("code", "bgp-meta-cwd", j.cwd);
        cwdCode.title = j.cwd;
        meta.appendChild(cwdCode);
      }
      meta.appendChild(bgEl("span", "bgp-spacer"));
      if (!bgSt.canControl && j.state === "running") {
        meta.appendChild(bgEl("span", "bgp-stpill is-stop", wsT("js.bgexec.readonly")));
      }
      meta.appendChild(bgEl("span", "bgp-stpill " + bgDotClass(j), bgStateText(j)));
      if (bgDockMeta) bgDockMeta.replaceWith(meta);
      else bgDockEl.appendChild(meta);
      bgDockMeta = meta;
    }

    if (!bgTailEl) {
      bgTailEl = bgEl("pre", "bgp-tail");
      bgTailEl.setAttribute("role", "log");
      bgTailEl.setAttribute("aria-live", "off");
      bgTailEl.setAttribute("aria-label", wsT("js.xhud.out.label"));
      bgTailEl.tabIndex = 0;
      bgTailEl.appendChild(bgEl("span", "bgp-tail-empty", wsT("js.bgexec.tail.empty")));
      bgTailEl.addEventListener("scroll", function () {
        if (!bgTailEl) return;
        bgSt.follow = bgTailEl.scrollHeight - bgTailEl.scrollTop - bgTailEl.clientHeight < 12;
      });
      bgDockEl.appendChild(bgTailEl);
    }

    if (!bgTailFootEl) {
      bgTailFootEl = bgEl("div", "bgp-dock-foot");
      bgDockEl.appendChild(bgTailFootEl);
    }
    bgRenderTailFoot(j);
  }

  function bgRenderTailFoot(j) {
    if (!bgTailFootEl || !j) return;
    var running = j.state === "running";
    var written = Math.max(j.written || 0, bgTailState && bgTailState.id === j.id ? bgTailState.written : 0);
    var evicted = Math.max(j.evicted || 0, bgTailState && bgTailState.id === j.id ? bgTailState.evicted : 0);
    var sizeText = evicted > 0 ? wsT("js.bgexec.tail.evicted", bgFmtBytes(written), bgFmtBytes(evicted)) : bgFmtBytes(written);
    var key = JSON.stringify([running, j.id, sizeText]);
    if (bgTailFootEl.bgKey === key) return;
    bgTailFootEl.bgKey = key;
    bgTailFootEl.textContent = "";
    var left = bgEl("span", "bgp-foot-l");
    left.appendChild(bgEl("span", "bgp-dot " + (running ? "is-ok" : "is-stop")));
    left.appendChild(doc.createTextNode((running ? wsT("js.bgexec.tail.live") : wsT("js.bgexec.tail.ended")) + " \u00b7 " + j.id));
    bgTailFootEl.appendChild(left);
    bgTailFootEl.appendChild(bgEl("span", "bgp-foot-r", sizeText + " \u00b7 " + wsT("js.bgexec.hint")));
  }

  // --- live tail reading (only while open) -----------------------------------
  function bgTailStop() {
    clearTimeout(bgTailTimer);
    bgTailTimer = 0;
    bgTailGen++;
    if (bgTailAC) { try { bgTailAC.abort(); } catch (e) {} bgTailAC = null; }
    bgTailState = null;
  }

  function bgTailStart(j) {
    bgTailStop();
    if (!bgSt.open || !j || !bgTailEl) return;
    bgTailEl.textContent = "";
    bgTailEl.appendChild(bgEl("span", "bgp-tail-empty", wsT("js.bgexec.tail.empty")));
    var w = j.written || 0;
    var start = Math.max(0, w - BG_TAIL_BYTES);
    bgTailState = {
      id: j.id,
      next: start,
      written: w,
      evicted: j.evicted || 0,
      delay: BG_POLL_MIN,
      drained: false,
      busy: false,
      open: false,
      openEl: null,
      openStream: "",
      skipFirstPartial: start > 0,
      gen: bgTailGen
    };
    bgTailSchedule(0);
  }

  function bgTailSchedule(ms) {
    clearTimeout(bgTailTimer);
    if (!bgSt.open || !bgTailState || bgTailState.drained || bgPageHidden()) return;
    var gen = bgTailState.gen;
    bgTailTimer = setTimeout(function () {
      bgTailTimer = 0;
      if (!bgSt.open || !bgTailState || bgTailState.gen !== gen || bgTailState.busy || bgPageHidden()) return;
      bgTailFetch(gen);
    }, ms);
  }

  function bgTailFetch(gen) {
    var ts = bgTailState;
    if (!ts || ts.gen !== gen || !bgSt.open || !rid || bgPageHidden()) return;
    ts.busy = true;
    var ac = window.AbortController ? new AbortController() : null;
    bgTailAC = ac;
    var url = "/s/" + rid + "/exec/bg/" + encodeURIComponent(ts.id) + "/output?from=" + encodeURIComponent(String(ts.next)) + "&limit=" + BG_TAIL_BYTES;
    var opts = { credentials: "same-origin", cache: "no-store" };
    if (ac) opts.signal = ac.signal;
    fetch(url, opts).then(function (r) {
      if (!r.ok) {
        var e = new Error("HTTP " + r.status);
        e.status = r.status;
        throw e;
      }
      return r.json();
    }).then(function (res) {
      if (bgTailAC === ac) bgTailAC = null;
      if (!bgTailState || bgTailState.gen !== gen || !bgSt.open) return;
      ts.busy = false;
      var advanced = typeof res.next === "number" && res.next > ts.next;
      if (typeof res.next === "number") ts.next = res.next;
      if (typeof res.written === "number") ts.written = res.written;
      if (typeof res.evicted === "number") ts.evicted = res.evicted;
      bgTailAppend(res);
      var j = bgSt.byId[ts.id];
      var wasRunning = j && j.state === "running";
      if (res.job && j) {
        j.state = res.job.state || j.state;
        j.exit_code = res.job.exit_code;
        j.signal = res.job.signal;
        j.written = ts.written;
        j.evicted = ts.evicted;
      }
      if (j) bgRenderTailFoot(j);
      var stillRunning = res.job ? res.job.state === "running" : (j && j.state === "running");
      if (wasRunning && !stillRunning) bgSyncTick();
      if (!stillRunning && !advanced) {
        ts.drained = true;
        bgSyncTick();
        return;
      }
      ts.delay = advanced ? BG_POLL_MIN : Math.min(BG_POLL_MAX, Math.round(ts.delay * 1.5));
      bgTailSchedule(stillRunning ? ts.delay : 100);
    }, function (err) {
      if (bgTailAC === ac) bgTailAC = null;
      if (!bgTailState || bgTailState.gen !== gen || !bgSt.open) return;
      ts.busy = false;
      if (err && err.name === "AbortError") return;
      if (err && (err.status === 404 || err.status === 403)) {
        ts.drained = true;
        if (err.status === 404) bgExecFetchList();
        return;
      }
      ts.delay = Math.min(BG_POLL_MAX, Math.round(ts.delay * 1.6));
      bgTailSchedule(ts.delay);
    });
  }

  function bgTailAppend(res) {
    var ts = bgTailState;
    if (!ts || !bgTailEl) return;
    var segs = Array.isArray(res.segments) ? res.segments : [];
    if (ts.skipFirstPartial && segs.length) {
      ts.skipFirstPartial = false;
      var idx = -1;
      for (var k = 0; k < segs.length; k++) {
        if (segs[k].text && segs[k].text.indexOf("\n") >= 0) { idx = k; break; }
      }
      if (idx >= 0) {
        segs = segs.slice(idx);
        segs[0] = { stream: segs[0].stream, text: segs[0].text.slice(segs[0].text.indexOf("\n") + 1) };
      }
    }
    var frag = doc.createDocumentFragment();
    var added = 0;
    if (res.gap) {
      frag.appendChild(bgEl("span", "bgp-line is-sys", wsT("js.bgexec.tail.gap")));
      ts.open = false;
      ts.openEl = null;
      added++;
    }
    segs.forEach(function (s) {
      if (!s || !s.text) return;
      var stream = s.stream === "stderr" ? "stderr" : "stdout";
      var parts = String(s.text).replace(/\r/g, "").split("\n");
      for (var i = 0; i < parts.length; i++) {
        var part = parts[i], last = i === parts.length - 1;
        var ln = ts.open && ts.openStream === stream ? ts.openEl : null;
        if (part !== "" || (!last && !ln)) {
          if (!ln) {
            ln = bgEl("span", "bgp-line" + (stream === "stderr" ? " is-err" : ""));
            frag.appendChild(ln);
            added++;
          }
          if (part !== "") ln.appendChild(doc.createTextNode(part));
        }
        if (!last) { ts.open = false; ts.openEl = null; }
        else if (part !== "") { ts.open = true; ts.openEl = ln; ts.openStream = stream; }
      }
    });
    if (frag.childNodes.length || added) {
      var emp = bgTailEl.querySelector(".bgp-tail-empty");
      if (emp && emp.parentNode) emp.parentNode.removeChild(emp);
      bgTailEl.appendChild(frag);
      while (bgTailEl.childNodes.length > BG_MAX_LINES) {
        var first = bgTailEl.firstChild;
        if (first === ts.openEl) { ts.open = false; ts.openEl = null; }
        bgTailEl.removeChild(first);
      }
      if (bgSt.follow) bgTailEl.scrollTop = bgTailEl.scrollHeight;
    }
  }

  function bgStopJob(id) {
    if (!bgSt.canControl || !rid || bgSt.stopping[id]) return;
    bgSt.stopping[id] = true;
    bgRenderAll();
    fetch("/s/" + rid + "/exec/bg/" + encodeURIComponent(id) + "/stop", {
      method: "POST",
      credentials: "same-origin",
      cache: "no-store"
    }).then(function (r) {
      if (!r.ok) return r.text().then(function (s) { throw new Error(s.trim() || ("HTTP " + r.status)); });
      return r.json();
    }).then(function () {
      delete bgSt.stopping[id];
      if (bgSt.open) bgExecFetchList();
    }, function (err) {
      delete bgSt.stopping[id];
      if (typeof noteWarn === "function") noteWarn(wsT("js.bgexec.stop.fail", (err && err.message) || "error"));
      if (bgSt.open) bgRenderAll();
    });
  }

  if (bgbadge) {
    bgbadge.addEventListener("click", function () {
      bgExecToggle();
    });
  }
