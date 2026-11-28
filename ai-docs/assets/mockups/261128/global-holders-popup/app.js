(function () {
  "use strict";

  var root = document.documentElement;
  var $ = function (id) { return document.getElementById(id); };
  var HOLD_MS = 1500;          // (1741)/(1742)=a: Hold-to-Confirm 1.5s, the only Stop UX
  var TICK_MS = 1500;          // live trace cadence, only while the popup is open and not paused
  var HIST_N = 24;
  var NOW = new Date("2026-11-28T12:11:00+09:00").getTime();

  var HD_CAUSE = { no_answer: "응답 없음", token: "토큰 불일치", protocol: "프로토콜 불일치" };

  // ---- seeded rng so fixtures look the same on every load ----
  var seed = 7;
  function rnd() { seed = (seed * 1664525 + 1013904223) >>> 0; return seed / 4294967296; }

  // ---- fixtures (shape mirrors /holders + process tree + RSS/CPU trace) ----
  function mins(n) { return new Date(NOW - n * 60000).toISOString(); }
  function proc(pid, ppid, name, rss, cpu) { return { pid: pid, ppid: ppid, name: name, rss: rss, cpu: cpu, baseRss: rss, baseCpu: cpu }; }
  function holder(o) {
    o.baseRss = o.rss; o.baseCpu = o.cpu;
    o.agents = o.agents || []; o.procs = o.procs || [];
    o.attached = o.attached !== false;
    return o;
  }
  function terms() {
    return {
      host0: [
        { id: "term-h0-a1", cwd: "/Users/kang-sw/devenv/agents-plugin-tool", state: "running", started_at: mins(95) },
        { id: "term-h0-b2", cwd: "/Users/kang-sw/devenv/ai-docs", state: "exited", started_at: mins(310) }
      ],
      orphans: [{ id: "term-or-c3", cwd: "/Users/kang-sw/gunpowder-odyssey/", state: "running", started_at: mins(47) }]
    };
  }
  function leadTree() {
    return holder({
      id: "h-main", kind: "lead", label: "메인 리드", harness: "agy", pid: 41208, started_at: mins(240), rss: 86, cpu: 1.4,
      agents: [
        holder({ id: "h-w1", kind: "agent", label: "worker: Phase 2 구현", harness: "agy", pid: 41955, started_at: mins(22), rss: 76, cpu: 0.3,
          procs: [proc(42301, 41955, "go test ./internal/wsdashboard", 98, 4.1), proc(42340, 41955, "chromium-headless", 210, 2.4)] }),
        holder({ id: "h-w2", kind: "agent", label: "worker: code-review-lite", harness: "claude", pid: 42110, started_at: mins(9), rss: 142, cpu: 1.2 })
      ]
    });
  }
  function scenarioData(name) {
    var t = terms();
    var d = { holders: [leadTree()], durable: { id: "durable-werk", label: "werk" }, failures: [], incomplete: false, can_stop: true, host0: t.host0, orphans: t.orphans };
    if (name === "empty") {
      d.holders = []; d.host0 = []; d.orphans = [];
      return d;
    }
    if (name === "healthy") {
      d.holders.push(holder({ id: "h-exec", kind: "exec", label: "exec · pnpm dev", harness: "", pid: 40877, started_at: mins(130), rss: 120, cpu: 2.2, restart_safe: true,
        procs: [proc(40902, 40877, "node vite --host", 164, 1.6)] }));
      d.host0 = [t.host0[0]]; d.orphans = [];
      return d;
    }
    // warning / noframe / incomplete
    d.holders.push(holder({ id: "h-exec", kind: "exec", label: "exec · go build 워처", pid: 40877, started_at: mins(130), rss: 380, cpu: 7.8, restart_safe: false }));
    d.holders.push(holder({ id: "h-feat2", kind: "lead", label: "기능 2", harness: "agy", pid: 48211, attached: false, cause: "no_answer", protocol: 3, started_at: mins(14), rss: 240, cpu: 1.0 }));
    d.holders.push(holder({ id: "h-art", kind: "lead", label: "", harness: "agy", pid: 47102, attached: false, cause: "token", protocol: 3, started_at: mins(62), rss: 190, cpu: 0.4 }));
    d.failures = [{ message: "holder dial /tmp/ws-holder-9f2c.sock: connection refused", at: mins(3) }];
    if (name === "incomplete") {
      d.holders = d.holders.filter(function (h) { return h.id !== "h-art"; });
      d.holders[d.holders.length - 1].cause = "protocol";
      d.incomplete = true;
    }
    return d;
  }

  // ---- metrics model ----
  function selfTotals(n) {
    var rss = n.rss, cpu = n.cpu;
    n.procs.forEach(function (p) { rss += p.rss; cpu += p.cpu; });
    n.agents.forEach(function (a) { var t = selfTotals(a); rss += t.rss; cpu += t.cpu; });
    return { rss: rss, cpu: cpu };
  }
  function seedHist(n) {
    var tot = selfTotals(n).cpu;
    n.hist = [];
    for (var i = 0; i < HIST_N; i++) n.hist.push(Math.max(0.1, tot * (0.55 + rnd() * 0.9)));
    n.agents.forEach(seedHist);
  }
  function allHolders(list) {
    var out = [];
    (function walk(l) { l.forEach(function (h) { out.push(h); walk(h.agents); }); })(list);
    return out;
  }
  function aggregate() {
    var rss = 0, cpu = 0;
    state.data.holders.forEach(function (h) { var t = selfTotals(h); rss += t.rss; cpu += t.cpu; });
    return { rss: rss, cpu: cpu };
  }
  function loadScenario(name) {
    var d = scenarioData(name);
    d.holders.forEach(seedHist);
    return d;
  }

  var state = {
    scenario: "warning",
    view: "session",
    data: null,
    expanded: { "h-main": true },
    confirming: null,
    open: null,
    hold: null,
    paused: false,
    timer: 0,
    ticks: 0
  };
  state.data = loadScenario("warning");

  // ---- utils ----
  function esc(s) {
    return String(s).replace(/[&<>"']/g, function (c) { return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]; });
  }
  function age(iso) {
    var m = Math.max(0, Math.round((NOW - new Date(iso).getTime()) / 60000));
    if (m < 1) return "just now";
    if (m < 60) return m + "m ago";
    var h = Math.floor(m / 60);
    return h < 24 ? h + "h ago" : Math.floor(h / 24) + "d ago";
  }
  function stamp(iso) {
    var d = new Date(iso), p = function (n) { return String(n).padStart(2, "0"); };
    return d.getFullYear() + "-" + p(d.getMonth() + 1) + "-" + p(d.getDate()) + " " + p(d.getHours()) + ":" + p(d.getMinutes());
  }
  function nowLabel() {
    var d = new Date(), p = function (n) { return String(n).padStart(2, "0"); };
    return p(d.getHours()) + ":" + p(d.getMinutes()) + ":" + p(d.getSeconds());
  }
  function fmtRss(mb) { return mb >= 1024 ? (mb / 1024).toFixed(2) + " GB" : Math.round(mb) + " MB"; }
  function fmtCpu(c) { return c.toFixed(1) + "%"; }
  function baseName(cwd, id) {
    if (!cwd) return id;
    return cwd.replace(/[\\/]+$/, "").split(/[\\/]/).pop() || cwd;
  }
  function frameActive() { return state.view === "session"; }
  var toastTimer = 0;
  function toast(msg) {
    var t = $("mini-toast");
    t.textContent = msg; t.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { t.hidden = true; }, 2600);
  }
  function note(msg, isErr) {
    var n = $("hdnote");
    n.textContent = msg || "";
    n.classList.toggle("err", !!isErr);
  }

  // ---- derived counts ----
  function counts() {
    var d = state.data, lost = 0, unsafe = 0, running = allHolders(d.holders).length;
    d.holders.forEach(function (h) {
      if (!h.attached) lost++;
      else if (h.restart_safe === false) unsafe++;
    });
    var warn = unsafe + (d.incomplete ? 1 : 0);
    return { running: running, lost: lost, unsafe: unsafe, fails: d.failures.length, warn: warn };
  }

  // ---- rendering ----
  var I = function (id, cls) { return '<svg class="' + (cls || "ic-xs") + '" aria-hidden="true"><use href="#i-' + id + '"/></svg>'; };

  function spark(n) {
    return '<svg class="spark" viewBox="0 0 48 16" preserveAspectRatio="none" aria-hidden="true"><polyline data-k="' + n.id + ':spark" points="' + sparkPoints(n.hist) + '"/></svg>';
  }
  function sparkPoints(h) {
    var max = Math.max.apply(null, h) * 1.15, pts = [];
    for (var i = 0; i < h.length; i++) pts.push((i * 48 / (h.length - 1)).toFixed(1) + "," + (14.5 - h[i] / max * 13).toFixed(1));
    return pts.join(" ");
  }
  function holdStop(id) {
    return '<span class="tn-act"><button type="button" class="hdstop danger d-hold-btn" data-act="holder-stop" data-id="' + esc(id) +
      '" title="미연결 홀더 중지 — 1.5초 길게 누르기">' + I("stop") + "Stop</button></span>";
  }

  function nodeRow(n, lv, opts) {
    var kids = n.agents.length + n.procs.length > 0;
    var lost = !n.attached;
    var label = n.label && n.label.trim() ? n.label : n.id;
    var t = selfTotals(n);
    var badges = "";
    if (n.harness) badges += '<span class="hb sm" data-h="' + n.harness + '"></span>';
    if (n.kind === "agent") badges += '<span class="tn-badge">하위 에이전트</span>';
    if (n.kind === "lead" && n.attached) badges += '<span class="tn-badge">리드</span>';
    if (n.kind === "exec") badges += '<span class="tn-badge">exec</span>';
    var meta = '<span class="pid">PID ' + n.pid + "</span>";
    if (n.started_at) meta += '<span title="started ' + stamp(n.started_at) + '">' + age(n.started_at) + "</span>";
    if (lost) meta += '<span class="hdcause">' + esc(HD_CAUSE[n.cause] || "알 수 없음") + "</span>";
    if (n.attached && n.restart_safe === false) meta += '<span class="hdunsafe" title="재시작 시 실행 중 작업이 유실될 수 있습니다">재시작 비안전</span>';
    var chev = kids
      ? '<button type="button" class="tn-chev" data-act="toggle" data-id="' + n.id + '" aria-expanded="' + (!!state.expanded[n.id]) + '" aria-label="하위 항목 펼치기/접기">' + I("chev") + "</button>"
      : '<span class="tn-chev"></span>';
    var act = lost && state.data.can_stop ? holdStop(n.id) : "<span></span>";
    var html = '<div class="tnode" data-node="' + n.id + '">' +
      '<div class="tn-row' + (kids ? " has-kids" : "") + (lost ? " lost" : "") + '" style="--lv:' + lv + '" data-id="' + n.id + '" title="' + esc(n.id) + '">' +
      chev + '<div class="tn-main"><div class="tn-l1"><span class="wtdot"></span><b class="wtname">' + esc(label) + "</b></div>" +
      '<div class="tn-meta">' + badges + meta + "</div></div>" +
      '<span class="tn-num" data-k="' + n.id + ':rss">' + fmtRss(t.rss) + "</span>" +
      '<span class="tn-num cpu" data-k="' + n.id + ':cpu">' + fmtCpu(t.cpu) + "</span>" +
      spark(n) + act + "</div>";
    if (kids) {
      html += '<div class="tn-kids" style="--kl:' + lv + '"' + (state.expanded[n.id] ? "" : " hidden") + ">";
      n.agents.forEach(function (a) { html += nodeRow(a, lv + 1); });
      n.procs.forEach(function (p) { html += procRow(p, lv + 1, n); });
      html += "</div>";
    }
    return html + "</div>";
  }
  function procRow(p, lv, parent) {
    p.id = "p-" + p.pid;
    return '<div class="tn-row os" style="--lv:' + lv + '" data-id="' + p.id + '" title="OS 하위 프로세스 · PID ' + p.pid + " → PPID " + p.ppid + '">' +
      '<span class="tn-chev"></span><div class="tn-main"><div class="tn-l1"><span class="wtdot"></span><b class="wtname" style="overflow:hidden;text-overflow:ellipsis;white-space:nowrap">' + esc(p.name) + "</b></div>" +
      '<div class="tn-meta"><span class="pid">PID ' + p.pid + " → " + p.ppid + "</span></div></div>" +
      '<span class="tn-num" data-k="' + p.id + ':rss">' + fmtRss(p.rss) + "</span>" +
      '<span class="tn-num cpu" data-k="' + p.id + ':cpu">' + fmtCpu(p.cpu) + "</span><span></span><span></span></div>";
  }
  function durableRow(du) {
    return '<div class="tnode"><div class="tn-row" style="--lv:0" data-id="' + du.id + '" title="werk 인프로세스 홀더 — 별도 OS 프로세스 없음">' +
      '<span class="tn-chev"></span><div class="tn-main"><div class="tn-l1"><span class="wtdot"></span><span class="hb sm" data-h="werk"></span><b class="wtname">' + esc(du.label) + '</b><span class="tn-badge dur">인프로세스</span></div>' +
      '<div class="tn-meta"><span>durable · OS 수 제외</span></div></div>' +
      '<span class="tn-num dim">—</span><span class="tn-num cpu">—</span><span></span><span></span></div></div>';
  }
  function failRow(f) {
    return '<div class="wtrow hdrow hdfail"><span class="wtdot"></span><span class="hdmsg" title="' + esc(f.message) + '">' + esc(f.message) +
      '</span><span class="wtage" title="failed ' + stamp(f.at) + '">' + age(f.at) + "</span></div>";
  }
  function termRow(t, group) {
    var exited = t.state === "exited";
    var name = baseName(t.cwd, t.id);
    var canAttach = frameActive() && !exited;
    var attachTitle = frameActive() ? "이 페이지에 터미널 카드로 연결" : "세션 프레임 활성화 시 연결 가능";
    var attach = exited ? "" :
      '<button type="button" class="hdstop primary" data-act="term-attach" data-id="' + esc(t.id) + '" data-group="' + group + '"' +
      (canAttach ? "" : " disabled") + ' title="' + esc(attachTitle) + '">' + I("link") + "Attach</button>";
    var stop = '<button type="button" class="hdstop danger" data-act="term-stop" data-id="' + esc(t.id) + '" data-group="' + group +
      '" title="' + (exited ? "종료된 터미널 제거" : "터미널 종료 (확인 필요)") + '">' + I("stop") + "Stop</button>";
    var meta = '<span class="hdcwd" title="' + esc(t.cwd) + '"><bdi>' + esc(t.cwd) + "</bdi></span>" + (exited ? '<span class="hdbadge">exited</span>' : "");
    var confirm = state.confirming === t.id
      ? '<div class="hdconfirm" role="alertdialog"><span>실행 중인 터미널을 종료할까요?</span>' +
        '<button type="button" class="hdstop danger" data-act="term-confirm-yes" data-id="' + esc(t.id) + '" data-group="' + group + '">종료</button>' +
        '<button type="button" class="hdstop" data-act="term-confirm-no">취소</button></div>'
      : "";
    return '<div class="wtrow hdrow hdterm' + (exited ? " exited" : "") + '" data-row="' + esc(t.id) + '" title="' + esc(t.id) + '">' +
      '<span class="wtdot"></span><span class="wtline"><b class="wtname">' + esc(name) + "</b>" +
      '<span class="wtage" title="started ' + stamp(t.started_at) + '">' + age(t.started_at) + "</span></span>" +
      '<span class="hdmeta">' + meta + '</span><span class="hdbtns">' + attach + stop + "</span>" + confirm + "</div>";
  }

  function render() {
    var d = state.data, c = counts(), html = "";
    var attention = d.holders.filter(function (h) { return !h.attached || h.restart_safe === false; });
    var running = d.holders.filter(function (h) { return h.attached && h.restart_safe !== false; });
    var head = '<div class="tn-head"><span></span><span>프로세스</span><span>RSS</span><span>CPU</span><span>추이</span><span></span></div>';

    if (d.incomplete) {
      html += '<div class="hp-banner" role="alert">' + I("warn", "ic") + "<span>일부 홀더 스캔이 미완료 상태입니다 — 새로 고침으로 재확인</span></div>";
    }
    if (attention.length || d.failures.length) {
      html += '<div class="hp-sec"><span class="splbl">주의 필요</span><span class="cnt">' + (attention.length + d.failures.length) + "</span></div>" + head;
      attention.forEach(function (h) { html += nodeRow(h, 0); });
      d.failures.forEach(function (f) { html += failRow(f); });
    }
    html += '<div class="hp-sec"><span class="splbl">실행 중 프로세스 트리</span><span class="cnt">' + running.reduce(function (a, h) { return a + 1 + h.agents.length; }, 0) + "</span></div>";
    if (running.length) {
      if (!attention.length && !d.failures.length) html += head;
      running.forEach(function (h) { html += nodeRow(h, 0); });
    } else {
      html += '<div class="wtempty quiet">실행 중인 홀더가 없습니다</div>';
    }
    html += durableRow(d.durable);

    html += '<div class="hp-sec"><span class="splbl">터미널</span><span class="cnt">' + (d.host0.length + d.orphans.length) + "</span></div>";
    if (d.host0.length) {
      html += '<div class="hp-sub">호스트 터미널 (host0)</div>';
      d.host0.forEach(function (t) { html += termRow(t, "host0"); });
    }
    if (d.orphans.length) {
      html += '<div class="hp-sub">고아 터미널</div>';
      d.orphans.forEach(function (t) { html += termRow(t, "orphan"); });
    }
    if (!d.host0.length && !d.orphans.length) html += '<div class="wtempty quiet">열린 터미널이 없습니다</div>';
    $("hdlist").innerHTML = html;

    renderChrome(c);
    updateMetrics();
  }

  // header chips + trigger pill (no list teardown)
  function renderChrome(c) {
    var chips = '<span class="hp-chip">실행 ' + c.running + "</span>";
    if (c.lost) chips += '<span class="hp-chip warn">미연결 ' + c.lost + "</span>";
    if (c.warn) chips += '<span class="hp-chip warn">주의 ' + c.warn + "</span>";
    if (c.fails) chips += '<span class="hp-chip fail">실패 ' + c.fails + "</span>";
    if (!c.lost && !c.warn && !c.fails && c.running) chips += '<span class="hp-chip ok">모두 정상</span>';
    $("hp-chips").innerHTML = chips;

    var pill = $("holders-trigger");
    var alert = c.lost || c.warn || c.fails;
    pill.dataset.state = alert ? "warn" : (c.running ? "ok" : "zero");
    $("hp-count").textContent = String(c.running);
    var detail = "실행 중 홀더 " + c.running;
    if (c.lost) detail += " · 미연결 " + c.lost;
    if (c.warn) detail += " · 주의 " + c.warn;
    if (c.fails) detail += " · 실패 " + c.fails;
    pill.title = detail; pill.setAttribute("aria-label", "Holders — " + detail);
  }

  // keyed in-place metric update: never rebuilds rows, so a held Stop button survives
  function setText(key, text) {
    var el = $("hdlist").querySelector('[data-k="' + key + '"]');
    if (el && el.textContent !== text) el.textContent = text;
  }
  function updateMetrics() {
    state.data.holders.forEach(function walk(h) {
      var t = selfTotals(h);
      setText(h.id + ":rss", fmtRss(t.rss));
      setText(h.id + ":cpu", fmtCpu(t.cpu));
      var sp = $("hdlist").querySelector('[data-k="' + h.id + ':spark"]');
      if (sp) sp.setAttribute("points", sparkPoints(h.hist));
      h.procs.forEach(function (p) { setText("p-" + p.pid + ":rss", fmtRss(p.rss)); setText("p-" + p.pid + ":cpu", fmtCpu(p.cpu)); });
      h.agents.forEach(walk);
    });
    var a = aggregate();
    var agg = $("hp-agg");
    agg.querySelector('[data-k="agg:rss"]').textContent = fmtRss(a.rss);
    agg.querySelector('[data-k="agg:cpu"]').textContent = fmtCpu(a.cpu);
  }
  function jitter(n) {
    n.cpu = Math.max(0.05, n.baseCpu * (0.6 + rnd() * 0.8));
    n.rss = Math.max(8, n.baseRss * (0.96 + rnd() * 0.08));
  }
  function tick() {
    state.ticks++;
    allHolders(state.data.holders).forEach(function (h) {
      jitter(h);
      h.procs.forEach(jitter);
    });
    allHolders(state.data.holders).forEach(function (h) {
      h.hist.push(selfTotals(h).cpu);
      if (h.hist.length > HIST_N) h.hist.shift();
    });
    updateMetrics();
  }

  // ---- live trace timer (open + not paused only) ----
  function syncTrace() {
    var open = state.open === "holders";
    var live = open && !state.paused;
    if (live && !state.timer) state.timer = setInterval(tick, TICK_MS);
    if (!live && state.timer) { clearInterval(state.timer); state.timer = 0; }
    var pill = $("trace-pill");
    pill.dataset.on = live ? "true" : "false";
    $("trace-lbl").textContent = state.paused ? "트레이스 일시정지" : "1.5s 트레이스";
    var pb = $("hp-pause");
    pb.setAttribute("aria-pressed", state.paused ? "true" : "false");
    pb.title = state.paused ? "트레이스 재개" : "트레이스 일시정지";
    pb.setAttribute("aria-label", pb.title);
    pb.firstElementChild.firstElementChild.setAttribute("href", state.paused ? "#i-play" : "#i-pause");
  }
  window.__hp = { ticks: function () { return state.ticks; }, timerOn: function () { return !!state.timer; } };

  // ---- actions ----
  function findTerm(id) {
    return state.data.host0.concat(state.data.orphans).filter(function (t) { return t.id === id; })[0];
  }
  function removeTerm(id) {
    state.data.host0 = state.data.host0.filter(function (t) { return t.id !== id; });
    state.data.orphans = state.data.orphans.filter(function (t) { return t.id !== id; });
  }
  function fakeFetch(then) {
    var pill = $("holders-trigger"), body = $("hdlist"), btn = $("hp-refresh");
    pill.classList.add("is-busy"); body.classList.add("is-loading"); btn.classList.add("spin");
    setTimeout(function () {
      pill.classList.remove("is-busy"); body.classList.remove("is-loading"); btn.classList.remove("spin");
      then();
    }, 650);
  }
  function doRefresh() {
    note("");
    fakeFetch(function () { render(); note("새로 고침 완료 · " + nowLabel() + " (GET /holders)"); });
  }
  function holderStop(id) {
    var h = state.data.holders.filter(function (x) { return x.id === id; })[0];
    if (!h) return;
    var row = document.querySelector('[data-node="' + id + '"]');
    note("");
    if (row) row.classList.add("leaving");
    setTimeout(function () {
      state.data.holders = state.data.holders.filter(function (x) { return x.id !== id; });
      fakeFetch(function () {
        render();
        note("홀더 " + (h.label || h.id) + " 중지됨 (PID " + h.pid + ") · " + nowLabel());
      });
    }, 220);
  }
  function termStop(id, group) {
    var t = findTerm(id);
    if (!t) return;
    state.confirming = null;
    note("");
    removeTerm(id);
    fakeFetch(function () { render(); note("터미널 " + baseName(t.cwd, t.id) + " 종료됨 (" + group + ") · " + nowLabel()); });
  }
  function toggleNode(id) {
    state.expanded[id] = !state.expanded[id];
    var node = document.querySelector('[data-node="' + id + '"]');
    if (!node) return;
    var chev = node.querySelector(":scope > .tn-row .tn-chev"), kids = node.querySelector(":scope > .tn-kids");
    if (chev) chev.setAttribute("aria-expanded", String(!!state.expanded[id]));
    if (kids) kids.hidden = !state.expanded[id];
  }

  // ---- hold-to-confirm (1.5s) ----
  function holdStart(btn) {
    if (state.hold || btn.disabled) return;
    btn.classList.add("holding");
    state.hold = {
      btn: btn,
      timer: setTimeout(function () {
        var b = state.hold && state.hold.btn;
        state.hold = null;
        if (!b) return;
        b.classList.remove("holding");
        b.classList.add("d-hold-ready");
        setTimeout(function () { if (b.isConnected) holderStop(b.dataset.id); }, 120);
      }, HOLD_MS)
    };
  }
  function holdCancel() {
    if (!state.hold) return;
    clearTimeout(state.hold.timer);
    state.hold.btn.classList.remove("holding");
    state.hold = null;
  }

  var list = $("hdlist");
  list.addEventListener("pointerdown", function (e) {
    var b = e.target.closest('[data-act="holder-stop"]');
    if (b && e.button === 0) holdStart(b);
  });
  list.addEventListener("pointerout", function (e) {
    var b = e.target.closest && e.target.closest('[data-act="holder-stop"]');
    if (b && state.hold && state.hold.btn === b && !b.contains(e.relatedTarget)) holdCancel();
  });
  document.addEventListener("pointerup", holdCancel);
  document.addEventListener("pointercancel", holdCancel);
  window.addEventListener("blur", holdCancel);
  document.addEventListener("visibilitychange", holdCancel);
  list.addEventListener("keydown", function (e) {
    var b = e.target.closest && e.target.closest('[data-act="holder-stop"]');
    if (b && (e.key === " " || e.key === "Enter") && !e.repeat) { e.preventDefault(); holdStart(b); }
  });
  list.addEventListener("keyup", function (e) { if (e.key === " " || e.key === "Enter") holdCancel(); });
  list.addEventListener("focusout", holdCancel);

  list.addEventListener("click", function (e) {
    var b = e.target.closest("[data-act]");
    if (!b) {
      var row = e.target.closest(".tn-row.has-kids");
      if (row) toggleNode(row.dataset.id);
      return;
    }
    if (b.disabled) return;
    var act = b.dataset.act, id = b.dataset.id;
    if (act === "holder-stop") {
      note("길게 눌러(1.5초) 중지를 확인하세요.");
    } else if (act === "toggle") {
      toggleNode(id);
    } else if (act === "term-attach") {
      var t = findTerm(id);
      if (!frameActive()) return;
      toast("postMessage → #frame  " + JSON.stringify({ type: "ws:term-attach", id: id, cwd: t && t.cwd }));
      note("터미널 " + baseName(t && t.cwd, id) + " 연결 요청 전송 · " + nowLabel());
    } else if (act === "term-stop") {
      var tt = findTerm(id);
      if (tt && tt.state !== "exited") { state.confirming = id; render(); var yes = list.querySelector('[data-act="term-confirm-yes"]'); if (yes) yes.focus(); }
      else termStop(id, b.dataset.group);
    } else if (act === "term-confirm-yes") {
      termStop(id, b.dataset.group);
    } else if (act === "term-confirm-no") {
      state.confirming = null; render(); note("종료를 취소했습니다.");
    }
  });

  // ---- popovers: mutual exclusion + placement ----
  var POPS = {
    holders: { pop: "holders-pop", trig: "holders-trigger", align: "right" },
    usage: { pop: "usage-pop", trig: "usage-trigger", align: "right" },
    gmenu: { pop: "gmenu", trig: "gear", align: "left" }
  };
  function place(key) {
    var p = POPS[key], pop = $(p.pop), tr = $(p.trig), bar = $("shell-bottom");
    var r = tr.getBoundingClientRect(), br = bar.getBoundingClientRect(), w = pop.offsetWidth;
    var left = p.align === "right" ? r.right - w : r.left;
    left = Math.max(8, Math.min(left, window.innerWidth - w - 8));
    pop.style.left = left + "px";
    pop.style.bottom = Math.round(window.innerHeight - br.top + 4) + "px";
  }
  function closeAll(except) {
    Object.keys(POPS).forEach(function (k) {
      if (k === except) return;
      $(POPS[k].pop).hidden = true;
      $(POPS[k].trig).setAttribute("aria-expanded", "false");
    });
    if (except !== "holders") { state.confirming = null; holdCancel(); }
    state.open = except || null;
    syncTrace();
  }
  function openPop(key) {
    closeAll(key);
    $(POPS[key].pop).hidden = false;
    $(POPS[key].trig).setAttribute("aria-expanded", "true");
    state.open = key;
    if (key === "holders") { render(); $("hdlist").scrollTop = 0; }
    place(key);
    syncTrace();
  }
  function toggle(key) { state.open === key ? closeAll() : openPop(key); }

  $("holders-trigger").addEventListener("click", function () { toggle("holders"); });
  $("usage-trigger").addEventListener("click", function () { toggle("usage"); });
  $("gear").addEventListener("click", function () { toggle("gmenu"); });
  $("hp-close").addEventListener("click", function () { closeAll(); $("holders-trigger").focus(); });
  $("hp-refresh").addEventListener("click", doRefresh);
  $("hp-pause").addEventListener("click", function () { state.paused = !state.paused; syncTrace(); note(state.paused ? "트레이스 일시정지됨" : "트레이스 재개 · 1.5초 주기"); });
  document.querySelectorAll("[data-close]").forEach(function (b) { b.addEventListener("click", function () { closeAll(); }); });
  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape" && state.open) {
      var tr = $(POPS[state.open].trig);
      if (state.hold) { holdCancel(); return; }
      closeAll();
      tr.focus();
    }
  });
  document.addEventListener("pointerdown", function (e) {
    if (!state.open) return;
    var p = POPS[state.open];
    if ($(p.pop).contains(e.target) || $(p.trig).contains(e.target)) return;
    closeAll();
  });
  window.addEventListener("resize", function () { if (state.open) place(state.open); });

  // ---- shell: views ----
  var currentSession = "feat2";
  var names = { main: ["메인", "#1"], feat1: ["기능 1", "#41"], feat2: ["기능 2", "#42"], art: ["아트", "#39"] };
  function setView(v) {
    state.view = v;
    $("view-session").hidden = v !== "session";
    $("view-home").hidden = v !== "home";
    document.querySelectorAll(".rs").forEach(function (r) { r.classList.toggle("cur", v === "session" && r.dataset.session === currentSession); });
    $("btn-home").setAttribute("aria-pressed", v === "home" ? "true" : "false");
    if (state.open === "holders") render();
  }
  document.querySelectorAll(".rs").forEach(function (r) {
    r.addEventListener("click", function () {
      currentSession = r.dataset.session;
      $("top-name").textContent = names[currentSession][0];
      $("top-seq").textContent = names[currentSession][1];
      setView("session");
    });
  });
  $("btn-home").addEventListener("click", function () { setView(state.view === "home" ? "session" : "home"); });
  $("btn-config").addEventListener("click", function () { toast("전역 설정 (/settings) — 목업"); });
  $("talert-trigger").addEventListener("click", function () { toast("전역 알림 팝업 — 목업"); });

  // ---- theme / toolbar ----
  function markSeg(ctl, val) {
    document.querySelectorAll('[data-ctl="' + ctl + '"] .m-opt').forEach(function (b) {
      var on = b.dataset.val === val; b.classList.toggle("is-on", on); b.setAttribute("aria-pressed", on);
    });
  }
  function setTheme(t) {
    root.dataset.theme = t;
    markSeg("theme", t);
    document.querySelectorAll("[data-gm-theme]").forEach(function (b) { b.classList.toggle("is-on", b.dataset.gmTheme === t); });
    if (state.open) place(state.open);
  }
  document.querySelectorAll("[data-gm-theme]").forEach(function (b) { b.addEventListener("click", function () { setTheme(b.dataset.gmTheme); }); });

  function setScenario(s) {
    state.scenario = s;
    root.dataset.scenario = s;
    state.data = loadScenario(s === "noframe" ? "warning" : s);
    state.confirming = null; holdCancel(); note("");
    markSeg("scenario", s);
    if (s === "noframe") {
      $("home-note").textContent = "활성 세션 프레임이 없습니다. Attach는 비활성화됩니다 — 레일에서 세션을 선택하면 프레임이 열립니다.";
      setView("home");
    } else if (state.view === "home") {
      setView("session");
    }
    render();
    if (state.open) place(state.open);
  }
  function setViewport(v) {
    $("viewport").dataset.vp = v; markSeg("viewport", v);
    requestAnimationFrame(function () { if (state.open) place(state.open); });
  }
  document.querySelectorAll(".m-seg").forEach(function (seg) {
    seg.addEventListener("click", function (e) {
      var b = e.target.closest(".m-opt");
      if (!b) return;
      var ctl = seg.dataset.ctl, v = b.dataset.val;
      if (ctl === "theme") setTheme(v);
      else if (ctl === "viewport") setViewport(v);
      else if (ctl === "scenario") setScenario(v);
    });
  });

  render();
  syncTrace();
})();
