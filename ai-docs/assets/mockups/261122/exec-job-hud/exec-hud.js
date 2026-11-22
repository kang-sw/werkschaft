/* Exec-job HUD mockup logic. Pure simulation: no network, no product code.
   ExecHud.create(host, opts) builds one conversation pane (header, messages,
   composer, the badge strip in the 10px gap) inside host. The live page uses
   timers; gallery cells pass frozen:true and a fixed clock so they render the
   same every time. */
(function () {
  "use strict";

  /* ------------------------------------------------------------ i18n */
  var I18N = {
    en: {
      "pane.title": "Conversation", "pane.lead": "lead", "pane.turn": "turn 14",
      "msg.me": "Run the web tests in watch mode, then check the build.",
      "msg.ag": "Started both. The watcher is running in the background; I will read its output when the build finishes.",
      "msg.card": "!-command card",
      "cin.ph": "Message, or ! to run a command", "sb.agents": "3 agents", "sb.running": "running",
      "toolbar": "Exec jobs", "ib.stop": "Interrupt",
      "own.agent": "Agent", "own.user": "You · !", "own.user.long": "You (composer !)", "own.agent.long": "Agent",
      "st.running": "Running", "st.stopping": "Stopping…", "st.ok": "Exited 0", "st.failed": "Failed · exit {0}",
      "st.sig": "Killed · signal {0}", "st.stopped.user": "Stopped by you", "st.stopped.agent": "Stopped by agent",
      "st.lost": "Lost", "st.nostart": "Never started",
      "st.short.ok": "ok", "st.short.failed": "exit {0}", "st.short.stopped": "stopped", "st.short.lost": "lost", "st.short.nostart": "no start", "st.short.stopping": "stopping",
      "k.cmd": "Command", "k.cwd": "Directory", "k.el": "Elapsed", "k.ran": "Ran", "k.started": "Started", "k.owner": "Launched by", "k.id": "Job",
      "tt.clip": "… {0} more lines — see the output panel",
      "tt.hint.run": "Click: live output", "tt.hint.user": "Click: live output · card in chat",
      "tt.note.lost": "Its holder is gone and left no exit status; the job may still be running.",
      "tt.note.nostart": "The process never started.",
      "tt.note.stopped.user": "Stopped by you · agent notified.", "tt.note.stopped.user.nonotice": "Stopped by you · no agent notice (your own command).",
      "tt.more.title": "{0} more exec jobs", "tt.agg.title": "{0} exec jobs",
      "chip.aria": "Exec job {0}, {1}", "chip.stop.aria": "Stop job {0}", "chip.dismiss.aria": "Dismiss {0}",
      "more.aria": "{0} more exec jobs", "agg.aria": "{0} exec jobs, open the list", "agg.ended": "{0} ended",
      "stop": "Stop", "stop.job": "Stop job", "cancel": "Cancel", "stopping": "Stopping",
      "conf.title": "Force-stop this job?",
      "conf.body": "Kills the job's whole process group at once (SIGKILL, no cleanup). Anything it had not saved is lost.",
      "conf.agent": "{0} gets a steer notice that you stopped it.", "conf.user": "Your own command: no agent notice is sent; the agent only sees a “stopped” receipt on its next turn.",
      "conf.auto": "Reverts in {0}s",
      "pk.label": "Exec job output", "pk.tabs": "Exec jobs", "pk.close": "Close (Esc)", "pk.pin": "Pin: ignore outside clicks",
      "pk.jump": "Jump to card", "pk.copy": "Copy command", "pk.copied": "Copied", "pk.expand": "Click to expand the command",
      "out.label": "Combined stdout/stderr, newest last", "out.empty": "(no output yet)", "out.new": "↓ {0} new", "out.follow": "Follow",
      "out.wrap": "Wrap", "out.live": "live", "out.paused": "paused — scrolled up", "out.ended": "ended",
      "out.tail": "tail · {0} of {1} written", "pk.foot": "Esc closes · ← → switch jobs",
      "out.killed": "— killed by you (SIGKILL on the process group) —",
      "out.exit": "— exited with code {0} —", "out.sigexit": "— killed by signal {0} —",
      "nt.sending": "Notifying agent…", "nt.steered": "Agent notified — delivered into its running turn", "nt.woken": "Agent notified — idle agent woken with a new turn",
      "nt.closed": "Agent session is closed — nobody to notify", "nt.failed": "Notice not delivered", "nt.retry": "Retry",
      "nt.none": "No agent notice — your own command", "nt.sample": "Notice text the agent receives (sample)", "nt.lang": "Sample is sent in the agent session's language, not the UI language.",
      "toast.agent": "Stopped {0} · agent notified", "toast.agent.fail": "Stopped {0} · agent NOT notified", "toast.user": "Stopped {0}", "toast.view": "View",
      "dismiss": "Dismiss",
      "ctl.theme": "Theme", "ctl.lang": "Language", "ctl.scn": "Scenario", "ctl.w": "Width", "ctl.agent": "Agent is",
      "ctl.view": "View", "ctl.live": "Live", "ctl.gallery": "All states",
      "scn.idle": "Idle", "scn.one": "1 job", "scn.many": "Many (7)", "scn.states": "Ended states",
      "w.desk": "Desktop", "w.narrow": "Narrow", "ag.working": "Working", "ag.idle": "Idle", "ag.closed": "Closed", "ag.down": "Unreachable",
      "ctl.add.agent": "+ agent job", "ctl.add.user": "+ ! job"
    },
    ko: {
      "pane.title": "대화", "pane.lead": "lead", "pane.turn": "턴 14",
      "msg.me": "웹 테스트를 watch 모드로 돌려 놓고 빌드도 확인해 줘.",
      "msg.ag": "둘 다 시작했습니다. watcher는 백그라운드에서 돌고 있고, 빌드가 끝나면 출력을 읽겠습니다.",
      "msg.card": "! 명령 카드",
      "cin.ph": "메시지 입력 · ! 로 명령 실행", "sb.agents": "에이전트 3", "sb.running": "실행 중",
      "toolbar": "exec 작업", "ib.stop": "중단",
      "own.agent": "에이전트", "own.user": "사용자 · !", "own.user.long": "사용자 (입력창 !)", "own.agent.long": "에이전트",
      "st.running": "실행 중", "st.stopping": "중지하는 중…", "st.ok": "정상 종료 (0)", "st.failed": "실패 · 종료 코드 {0}",
      "st.sig": "시그널 {0}로 종료", "st.stopped.user": "사용자가 중지함", "st.stopped.agent": "에이전트가 중지함",
      "st.lost": "유실됨", "st.nostart": "시작되지 않음",
      "st.short.ok": "완료", "st.short.failed": "코드 {0}", "st.short.stopped": "중지됨", "st.short.lost": "유실", "st.short.nostart": "시작 안 됨", "st.short.stopping": "중지 중",
      "k.cmd": "명령", "k.cwd": "디렉터리", "k.el": "경과", "k.ran": "실행 시간", "k.started": "시작", "k.owner": "실행 주체", "k.id": "작업",
      "tt.clip": "… {0}줄 더 있음 — 출력 패널에서 확인",
      "tt.hint.run": "클릭: 실시간 출력", "tt.hint.user": "클릭: 실시간 출력 · 채팅에 카드 있음",
      "tt.note.lost": "홀더가 사라졌고 종료 상태가 남지 않았습니다. 작업이 아직 실행 중일 수 있습니다.",
      "tt.note.nostart": "프로세스가 시작되지 않았습니다.",
      "tt.note.stopped.user": "사용자가 중지함 · 에이전트에 알림 전달됨.", "tt.note.stopped.user.nonotice": "사용자가 중지함 · 에이전트 알림 없음 (직접 실행한 명령).",
      "tt.more.title": "exec 작업 {0}개 더", "tt.agg.title": "exec 작업 {0}개",
      "chip.aria": "exec 작업 {0}, {1}", "chip.stop.aria": "작업 {0} 중지", "chip.dismiss.aria": "{0} 닫기",
      "more.aria": "exec 작업 {0}개 더", "agg.aria": "exec 작업 {0}개, 목록 열기", "agg.ended": "종료 {0}",
      "stop": "중지", "stop.job": "작업 중지", "cancel": "취소", "stopping": "중지 중",
      "conf.title": "이 작업을 강제 중지할까요?",
      "conf.body": "작업의 프로세스 그룹 전체를 즉시 종료합니다 (SIGKILL, 정리 과정 없음). 저장하지 않은 내용은 사라집니다.",
      "conf.agent": "{0}에게 사용자가 중지했다는 steer 알림이 전달됩니다.", "conf.user": "직접 실행한 명령이라 에이전트에 알림을 보내지 않습니다. 다음 턴에 ‘중지됨’ 기록만 전달됩니다.",
      "conf.auto": "{0}초 후 되돌아감",
      "pk.label": "exec 작업 출력", "pk.tabs": "exec 작업", "pk.close": "닫기 (Esc)", "pk.pin": "고정: 바깥 클릭 무시",
      "pk.jump": "채팅의 카드로 이동", "pk.copy": "명령 복사", "pk.copied": "복사됨", "pk.expand": "클릭하여 명령 펼치기",
      "out.label": "stdout/stderr 합본, 최신이 아래", "out.empty": "(아직 출력 없음)", "out.new": "↓ 새 줄 {0}", "out.follow": "따라가기",
      "out.wrap": "줄바꿈", "out.live": "실시간", "out.paused": "일시정지 — 위로 스크롤됨", "out.ended": "종료됨",
      "out.tail": "끝부분 · 누적 {1} 중 {0}", "pk.foot": "Esc 닫기 · ← → 작업 전환",
      "out.killed": "— 사용자가 강제 종료함 (프로세스 그룹 SIGKILL) —",
      "out.exit": "— 종료 코드 {0}로 끝남 —", "out.sigexit": "— 시그널 {0}로 종료됨 —",
      "nt.sending": "에이전트에 알리는 중…", "nt.steered": "에이전트에 알림 — 진행 중인 턴에 전달됨", "nt.woken": "에이전트에 알림 — 대기 중이던 에이전트를 새 턴으로 깨움",
      "nt.closed": "에이전트 세션이 닫혀 있어 알릴 대상이 없음", "nt.failed": "알림이 전달되지 않음", "nt.retry": "다시 시도",
      "nt.none": "에이전트 알림 없음 — 직접 실행한 명령", "nt.sample": "에이전트가 받는 알림 본문 (예시)", "nt.lang": "예시는 UI 언어가 아니라 에이전트 세션의 언어로 전달됩니다.",
      "toast.agent": "{0} 중지됨 · 에이전트에 알림 전송", "toast.agent.fail": "{0} 중지됨 · 에이전트 알림 실패", "toast.user": "{0} 중지됨", "toast.view": "보기",
      "dismiss": "닫기",
      "ctl.theme": "테마", "ctl.lang": "언어", "ctl.scn": "시나리오", "ctl.w": "너비", "ctl.agent": "에이전트 상태",
      "ctl.view": "보기", "ctl.live": "라이브", "ctl.gallery": "전체 상태",
      "scn.idle": "없음", "scn.one": "1개", "scn.many": "여러 개 (7)", "scn.states": "종료 상태들",
      "w.desk": "데스크톱", "w.narrow": "좁게", "ag.working": "작업 중", "ag.idle": "대기", "ag.closed": "닫힘", "ag.down": "연결 불가",
      "ctl.add.agent": "+ 에이전트 작업", "ctl.add.user": "+ ! 작업"
    }
  };

  /* -------------------------------------------------- output scripts */
  var SCRIPTS = {
    test: [["o", "=== RUN   TestHostedLaunch"], ["o", "--- PASS: TestHostedLaunch (0.04s)"], ["o", "=== RUN   TestHostedStopOwner"], ["e", "warn: holder 7f3a slow to ack stop (210ms)"], ["o", "--- PASS: TestHostedStopOwner (0.31s)"], ["o", "=== RUN   TestHostedTailBounds"], ["o", "--- PASS: TestHostedTailBounds (0.01s)"], ["o", "ok  \tws/internal/execjob\t0.412s"]],
    watch: [[" o", " ✓ src/hud.test.ts (12 tests) 38ms"], ["o", " ✓ src/strip.test.ts (7 tests) 21ms"], ["o", " RERUN  src/strip.ts x2"], ["e", "stderr | src/strip.test.ts > fold > keeps one chip\nwarn: ResizeObserver loop limit exceeded"], ["o", " ✓ src/strip.test.ts (7 tests) 19ms"], ["o", " Waiting for file changes…"]],
    serve: [["o", "GET /api/tasks 200 4ms"], ["o", "GET /assets/app.css 304 1ms"], ["o", "POST /api/tasks 201 12ms"], ["e", "WARN slow query: tasks.list took 480ms"], ["o", "GET /api/tasks?page=2 200 9ms"]],
    make: [["o", "CC  src/core/alloc.c"], ["o", "CC  src/core/sched.c"], ["e", "src/core/sched.c:88:14: warning: unused variable 'slack' [-Wunused-variable]"], ["o", "CC  src/net/conn.c"], ["o", "LD  build/engine"], ["o", "Built target engine"]],
    fetch: [["o", "Fetching origin"], ["o", "remote: Enumerating objects: 214, done."], ["o", "remote: Counting objects: 100% (214/214), done."], ["o", "Receiving objects: 100% (214/214), 61.2 KiB | 4.1 MiB/s, done."], ["o", " * [new branch]      impl/develop/exec-hud -> origin/impl/develop/exec-hud"]],
    log: [["o", "21:47:02 ws[48211] hosted: job exec_calm_heron started"], ["o", "21:47:04 ws[48211] holder 7f3a attach ok"], ["e", "21:47:09 ws[48211] warn: rail wake coalesced (3)"], ["o", "21:47:12 ws[48211] sse region hash=ab12 sent"]]
  };

  var CMD = {
    watch: { cmd: "pnpm vitest --watch", cwd: "web/", script: "watch" },
    test: { cmd: "go test ./internal/execjob/... -run 'TestHosted' -count=1 -v", cwd: "./", script: "test" },
    serve: { cmd: "python3 -m http.server 8080 --directory ./ai-docs/assets/mockups", cwd: "./", script: "serve" },
    make: { cmd: "cd engine && make -j8 all 2>&1 | tee /tmp/build-engine.log", cwd: "engine/", script: "make" },
    fetch: { cmd: "git fetch --all --prune", cwd: "./", script: "fetch" },
    log: { cmd: "tail -f ~/.local/state/ws/daemon.log | grep -v heartbeat", cwd: "./", script: "log" },
    long: { cmd: "for f in $(git ls-files 'ai-docs/tickets/**/*.md'); do wc -l \"$f\"; done | sort -n | tail -n 20 && echo 'done counting tickets across every status directory' && sleep 600", cwd: "docs/", script: "log" },
    dev: { cmd: "npm run dev -- --port 5174", cwd: "web/", script: "serve" }
  };
  var NAMES = ["calm_heron", "quiet_otter", "amber_finch", "brisk_lynx", "mellow_crane", "tidy_marten", "vivid_stoat", "plain_wren", "hazy_ibis", "lucid_pika"];
  var nameIdx = 0;

  /* ---------------------------------------------------------- helpers */
  function el(tag, cls, text) { var e = document.createElement(tag); if (cls) e.className = cls; if (text != null) e.textContent = text; return e; }
  function fmtEl(ms) {
    var s = Math.max(0, Math.floor(ms / 1000)), h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), r = s % 60;
    function p(n) { return n < 10 ? "0" + n : "" + n; }
    return h ? h + ":" + p(m) + ":" + p(r) : m + ":" + p(r);
  }
  function fmtClock(ms) { var d = new Date(ms); function p(n) { return n < 10 ? "0" + n : "" + n; } return p(d.getHours()) + ":" + p(d.getMinutes()) + ":" + p(d.getSeconds()); }
  function fmtBytes(n) { return n < 1024 ? n + " B" : (n / 1024).toFixed(1) + " KiB"; }
  function cmdHead(cmd) {
    var c = cmd.trim().replace(/^cd\s+[^&;]+(&&|;)\s*/, "").replace(/^([A-Z_]+=\S+\s+)+/, "");
    var t = c.split(/\s+/), a = (t[0] || "").split("/").pop();
    if (t[1] && t[1].charAt(0) !== "-" && t[1].charAt(0) !== "'" && t[1].charAt(0) !== "\"") a += " " + t[1];
    return a;
  }
  function genLine(job) {
    var sc = SCRIPTS[job.script] || SCRIPTS.log, e = sc[job.cursor % sc.length]; job.cursor++;
    var s = e[0].trim(), text = e[1];
    if (job.script === "serve" && job.cursor > sc.length) text = text.replace(/\d+ms/, (1 + (job.cursor * 7) % 40) + "ms");
    return { s: s === "e" ? "e" : "o", text: text };
  }
  function pushLine(job, l) { job.lines.push(l); job.bytes += l.text.length + 1; if (job.lines.length > 600) job.lines.shift(); }

  /* ------------------------------------------------------------- hud */
  function create(host, o) {
    o = o || {};
    var frozen = !!o.frozen;
    var now0 = o.now || Date.now();
    var clock0 = Date.now();
    function now() { return frozen ? now0 : now0 + (Date.now() - clock0); }
    var S = {
      lang: o.lang || "en", narrow: !!o.narrow, agent: o.agent || "working",
      jobs: [], peek: false, peekId: "", confirm: "", pinned: false, follow: true, pending: 0, wrap: true,
      tipId: "", tipKind: "", toast: null, origin: null, subagents: o.subagents == null ? 3 : o.subagents, bg: o.bg || 0, cmdOpen: false, nseq: 0
    };
    function t(k) {
      var s = (I18N[S.lang] && I18N[S.lang][k]); if (s == null) s = I18N.en[k]; if (s == null) s = k;
      for (var i = 1; i < arguments.length; i++) s = s.replace("{" + (i - 1) + "}", arguments[i]);
      return s;
    }
    var timers = [];
    function later(fn, ms) { if (frozen) return 0; var id = setTimeout(fn, ms); timers.push(id); return id; }

    /* ----- job model ----- */
    function stOf(j) {
      if (j.state === "running") return "running";
      if (j.state === "stopping") return "stopping";
      if (j.state === "exited") return (j.exit === 0 && j.sig == null) ? "ok" : "failed";
      return j.state; // stopped | lost | nostart
    }
    function isEnded(j) { var s = stOf(j); return s !== "running" && s !== "stopping"; }
    function needsAttn(j) { var s = stOf(j); return s === "failed" || s === "lost" || s === "nostart" || s === "stopping"; }
    function addJob(spec) {
      var base = CMD[spec.kind] || CMD.watch;
      var j = {
        id: "exec_" + (spec.name || NAMES[nameIdx++ % NAMES.length]), kind: spec.kind, cmd: spec.cmd || base.cmd, cwd: spec.cwd || base.cwd,
        owner: spec.owner || "agent", state: spec.state || "running", exit: spec.exit, sig: spec.sig,
        startedAt: spec.startedAt != null ? spec.startedAt : now(), endedAt: spec.endedAt || 0, script: base.script, cursor: 0,
        lines: [], bytes: 0, finishAfter: spec.finishAfter || 0, finishExit: spec.finishExit || 0,
        stoppedBy: spec.stoppedBy || "", notice: spec.notice || null, dismissed: false, seq: S.nseq++
      };
      for (var i = 0; i < (spec.preLines == null ? 9 : spec.preLines); i++) pushLine(j, genLine(j));
      if (j.state === "exited" || j.state === "stopped") pushLine(j, endLine(j));
      S.jobs.push(j); return j;
    }
    function endLine(j) {
      if (j.stoppedBy === "user") return { s: "sys", text: t("out.killed") };
      if (j.sig != null) return { s: "sys", text: t("out.sigexit", j.sig) };
      return { s: "sys", text: t("out.exit", j.exit == null ? 0 : j.exit) };
    }
    function byId(id) { for (var i = 0; i < S.jobs.length; i++) if (S.jobs[i].id === id) return S.jobs[i]; return null; }
    function stateText(j) {
      var s = stOf(j);
      if (s === "running") return t("st.running");
      if (s === "stopping") return t("st.stopping");
      if (s === "ok") return t("st.ok");
      if (s === "failed") return j.sig != null ? t("st.sig", j.sig) : t("st.failed", j.exit);
      if (s === "stopped") return j.stoppedBy === "user" ? t("st.stopped.user") : t("st.stopped.agent");
      if (s === "lost") return t("st.lost");
      return t("st.nostart");
    }
    function tone(j) { var s = stOf(j); return s === "running" ? "" : s === "ok" ? "ok" : s === "failed" ? "fail" : s === "lost" || s === "nostart" ? "warn" : "mute"; }
    function shortText(j) {
      var s = stOf(j);
      if (s === "ok") return t("st.short.ok");
      if (s === "failed") return j.sig != null ? "sig " + j.sig : t("st.short.failed", j.exit);
      return t("st.short." + s);
    }
    function order() {
      var vis = S.jobs.filter(function (j) { return !j.dismissed; });
      function rank(j) { return needsAttn(j) || (isEnded(j) && now() - j.endedAt < 10000) ? 0 : !isEnded(j) ? 1 : 2; }
      return vis.sort(function (a, b) {
        var ra = rank(a), rb = rank(b); if (ra !== rb) return ra - rb;
        if (ra === 1) return a.startedAt - b.startedAt;
        return (b.endedAt || 0) - (a.endedAt || 0);
      });
    }
    function ownerLabel(j) { return j.owner === "user" ? t("own.user") : t("own.agent"); }

    /* ----- skeleton ----- */
    host.textContent = "";
    var pane = el("div", "pane" + (S.narrow ? " narrow" : "") + (o.tall ? " tall" : ""));
    if (o.paneW) pane.style.setProperty("--pane-w", o.paneW + "px");
    if (o.paneH) pane.style.setProperty("--pane-h", o.paneH + "px");
    var phead = el("div", "phead"); pane.appendChild(phead);
    var island = el("div", "island"); island.setAttribute("aria-hidden", "true"); pane.appendChild(island);
    var msgs = el("div", "msgs"); pane.appendChild(msgs);
    var cw = el("div", "cw"); pane.appendChild(cw);
    var peekEl = el("div", "xpeek"); peekEl.hidden = true; peekEl.setAttribute("role", "dialog"); peekEl.setAttribute("aria-modal", "false"); cw.appendChild(peekEl);
    var toastEl = el("div", "xtoast"); toastEl.hidden = true; toastEl.setAttribute("role", "status"); cw.appendChild(toastEl);
    var tipEl = el("div", "xtip"); tipEl.hidden = true; tipEl.setAttribute("role", "tooltip"); tipEl.id = "xtip-" + Math.random().toString(36).slice(2, 7); cw.appendChild(tipEl);
    var composer = el("div", "composer"); cw.appendChild(composer);
    var cb = el("div", "cbadges"); composer.appendChild(cb);
    var ib = el("button", "ibadge"); ib.type = "button"; ib.innerHTML = '<span class="istop"></span><span class="ilabel"></span>'; cb.appendChild(ib);
    var sbw = el("span", "sbwrap"); cb.appendChild(sbw);
    var group = el("span", "xgroup"); group.setAttribute("role", "toolbar"); sbw.appendChild(group);
    var bgb = el("span", "bgbadge"); sbw.appendChild(bgb);
    var sb = el("button", "sbadge"); sb.type = "button"; sbw.appendChild(sb);
    var cin = el("div", "cin"); composer.appendChild(cin);
    host.appendChild(pane);

    function renderChrome() {
      phead.textContent = "";
      var b = el("b", null, t("pane.title")); phead.appendChild(b);
      phead.appendChild(el("span", "chip", t("pane.turn")));
      phead.appendChild(el("span", "chip", S.lang === "ko" ? "실행 " + S.jobs.filter(function (j) { return !isEnded(j); }).length : "exec " + S.jobs.filter(function (j) { return !isEnded(j); }).length));
      island.textContent = "";
      [["claude", "Cl", "thinking"], ["codex", "Co", "tool"], ["agy", "Ag", "idle"]].forEach(function (n) {
        var nd = el("span", "hud-node"); nd.setAttribute("data-state", n[2]);
        nd.appendChild(el("span", "hud-ring")); var e = el("span", "hud-emblem", n[1]); e.setAttribute("data-h", n[0]); nd.appendChild(e); island.appendChild(nd);
      });
      msgs.textContent = "";
      var m1 = el("div", "msg me"); m1.appendChild(el("span", "who", S.lang === "ko" ? "나" : "you")); m1.appendChild(document.createTextNode(t("msg.me"))); msgs.appendChild(m1);
      var m2 = el("div", "msg"); m2.appendChild(el("span", "who", t("pane.lead"))); m2.appendChild(document.createTextNode(t("msg.ag"))); msgs.appendChild(m2);
      var ucards = S.jobs.filter(function (j) { return j.owner === "user"; }).slice(-1);
      ucards.forEach(function (j) {
        var c = el("div", "cmdcard"); c.setAttribute("data-job", j.id);
        var top = el("div", "cc-top"); top.appendChild(el("span", "cc-tag", t("msg.card"))); top.appendChild(el("code", null, "! " + j.cmd.slice(0, 60)));
        var sp = el("span", "pill" + (tone(j) ? " " + tone(j) : ""), stateText(j)); top.appendChild(sp); c.appendChild(top);
        c.appendChild(el("span", "cc-note", S.lang === "ko" ? "(command-slots 티켓의 카드 자리표시 — 이 목업은 카드를 재설계하지 않음)" : "(placeholder for the command-slots card; not redesigned here)"));
        msgs.appendChild(c);
      });
      cin.textContent = ""; cin.appendChild(el("kbd", null, "!")); cin.appendChild(el("span", null, t("cin.ph"))); cin.appendChild(el("span", "send"));
      ib.hidden = S.agent !== "working"; ib.querySelector(".ilabel").textContent = t("ib.stop");
      if (S.subagents > 0) { sb.hidden = false; sb.innerHTML = '<span class="dotspin"></span><span class="slabel"></span><span class="sword"></span>'; sb.querySelector(".slabel").textContent = t("sb.agents"); sb.querySelector(".sword").textContent = t("sb.running"); } else sb.hidden = true;
      if (S.bg) { bgb.hidden = false; bgb.textContent = "◷ " + S.bg + " bg"; } else bgb.hidden = true;
      group.setAttribute("aria-label", t("toolbar"));
    }

    /* ----- strip: chips, +N, aggregate; fit by measuring ----- */
    var slots = [], moreBtn = null, aggBtn = null, shown = [], foldedJobs = [];
    function nodeEl(j, big) {
      var n = el("span", "xnode"); n.setAttribute("data-st", stOf(j));
      var e = el("span", "xemb" + (j.owner === "user" ? " user" : ""), j.owner === "user" ? "!" : ">_"); n.appendChild(e);
      n.appendChild(el("span", "xring"));
      var st = stOf(j), bd = null;
      if (st === "ok") bd = ["ok", "✓"]; else if (st === "failed") bd = ["fail", "×"]; else if (st === "lost") bd = ["warn", "?"]; else if (st === "nostart") bd = ["warn", "!"]; else if (st === "stopped") bd = ["mute", "■"];
      if (bd) n.appendChild(el("span", "xbadge " + bd[0], bd[1]));
      return n;
    }
    function buildChip(j) {
      var slot = el("span", "xslot"); slot.setAttribute("data-id", j.id);
      var b = el("button", "xchip" + (isEnded(j) ? " ended" : "")); b.type = "button"; b.setAttribute("data-id", j.id); b.setAttribute("data-st", stOf(j));
      b.setAttribute("aria-haspopup", "dialog"); b.setAttribute("aria-expanded", S.peek && S.peekId === j.id ? "true" : "false");
      b.setAttribute("aria-label", t("chip.aria", cmdHead(j.cmd), stateText(j) + ", " + ownerLabel(j)));
      b.appendChild(nodeEl(j));
      b.appendChild(el("span", "xlab", cmdHead(j.cmd)));
      if (stOf(j) === "running") { var e = el("span", "xel", fmtEl(now() - j.startedAt)); e.setAttribute("data-since", j.startedAt); b.appendChild(e); }
      else b.appendChild(el("span", "xst " + (tone(j) || "mute"), stOf(j) === "stopping" ? t("st.short.stopping") : shortText(j)));
      slot.appendChild(b);
      var c = el("button", "xstop"); c.type = "button"; c.tabIndex = -1; c.setAttribute("data-id", j.id);
      if (isEnded(j)) { c.textContent = "×"; c.setAttribute("data-act", "dismiss"); c.setAttribute("aria-label", t("chip.dismiss.aria", cmdHead(j.cmd))); c.title = t("dismiss"); }
      else { c.innerHTML = "<i></i>"; c.setAttribute("data-act", "stop"); c.setAttribute("aria-label", t("chip.stop.aria", cmdHead(j.cmd))); c.title = t("stop.job"); if (stOf(j) === "stopping") c.hidden = true; }
      slot.appendChild(c);
      return slot;
    }
    function worstAttn(list) {
      var w = null; list.forEach(function (j) { var s = stOf(j); if (s === "failed") w = ["fail", "×"]; else if ((s === "lost" || s === "nostart") && (!w || w[0] !== "fail")) w = ["warn", "?"]; });
      return w;
    }
    function renderStrip() {
      group.textContent = ""; slots = []; var list = order();
      list.forEach(function (j) { var s = buildChip(j); slots.push({ job: j, el: s }); group.appendChild(s); });
      moreBtn = el("button", "xmore"); moreBtn.type = "button"; moreBtn.setAttribute("data-kind", "more"); moreBtn.setAttribute("aria-haspopup", "dialog"); group.appendChild(moreBtn);
      aggBtn = el("button", "xchip xagg"); aggBtn.type = "button"; aggBtn.setAttribute("data-kind", "agg"); aggBtn.setAttribute("aria-haspopup", "dialog"); group.appendChild(aggBtn);
      group.hidden = list.length === 0 && !S.jobs.length; lastSig = list.map(function (j) { return j.id; }).join(); layoutStrip();
    }
    function layoutStrip() {
      if (!moreBtn) return;
      var list = slots.map(function (s) { return s.job; });
      slots.forEach(function (s) { s.el.hidden = false; }); moreBtn.hidden = true; aggBtn.hidden = true;
      var running = S.jobs.filter(function (j) { return !isEnded(j); });
      if (!list.length) {
        // nothing to show as chips: a quiet history chip only when ended jobs exist
        var ended = S.jobs.length;
        if (ended) { aggBtn.hidden = false; aggBtn.className = "xchip xagg quiet"; aggBtn.innerHTML = ""; aggBtn.appendChild(quietNode()); aggBtn.appendChild(el("span", "xst mute", t("agg.ended", ended))); aggBtn.setAttribute("aria-label", t("agg.aria", ended)); shown = []; foldedJobs = S.jobs.slice(); }
        else { shown = []; foldedJobs = []; }
        return;
      }
      var gap = 6, cbw = cb.clientWidth, ibw = ib.offsetWidth + 8;
      var sib = 0, cnt = 0;[bgb, sb].forEach(function (x) { if (!x.hidden) { sib += Math.max(x.offsetWidth, x.scrollWidth); cnt++; } });
      var avail = cbw - ibw - sib - cnt * gap;
      var w = slots.map(function (s) { return s.el.offsetWidth; });
      moreBtn.hidden = false; moreBtn.textContent = "+99"; var mw = moreBtn.offsetWidth; moreBtn.hidden = true;
      var n = list.length;
      function total(k) { var sum = 0; for (var i = 0; i < k; i++) sum += w[i] + (i ? gap : 0); if (k < list.length) sum += (k ? gap : 0) + mw; return sum; }
      while (n > 1 && total(n) > avail) n--;
      if (n === 1 && total(1) > avail) n = 0;
      if (o.forceAgg) n = 0;
      if (o.maxChips != null) n = Math.min(n, o.maxChips);
      shown = list.slice(0, n); foldedJobs = list.slice(n);
      slots.forEach(function (s, i) { s.el.hidden = i >= n; });
      var attn = worstAttn(list);
      aggBtn.className = "xchip xagg"; aggBtn.innerHTML = "";
      var lead = list.filter(needsAttn)[0] || running[0] || list[0];
      aggBtn.appendChild(nodeEl(lead));
      aggBtn.appendChild(el("span", "xn", String(list.length)));
      if (attn) aggBtn.appendChild(el("span", "xst " + attn[0], attn[1]));
      aggBtn.setAttribute("aria-label", t("agg.aria", list.length));
      if (n === 0) aggBtn.hidden = false;
      else if (foldedJobs.length) {
        moreBtn.hidden = false; moreBtn.textContent = "+" + foldedJobs.length; moreBtn.setAttribute("aria-label", t("more.aria", foldedJobs.length));
        var at2 = worstAttn(foldedJobs); if (at2) moreBtn.appendChild(el("span", "xbadge " + at2[0], at2[1]));
      }
    }
    function quietNode() { var n = el("span", "xnode"); n.appendChild(el("span", "xemb", ">_")); return n; }

    /* ----- tooltip ----- */
    function showTip(kind, id, anchor) {
      S.tipId = id || ""; S.tipKind = kind;
      tipEl.textContent = "";
      if (kind === "job") {
        var j = byId(id); if (!j) return hideTip();
        var head = el("div", "tt-head");
        head.appendChild(nodeEl(j)); head.appendChild(el("span", "tt-title", j.id)); head.appendChild(el("span", "tt-own", ownerLabel(j)));
        head.appendChild(el("span", "tt-pill " + tone(j), stateText(j))); tipEl.appendChild(head);
        var lines = j.cmd.split("\n"), pre = el("pre", "tt-cmd"); pre.textContent = j.cmd; tipEl.appendChild(pre);
        var kv = el("dl", "tt-kv");
        function row(k, v, mono) { kv.appendChild(el("dt", null, t(k))); var dd = el("dd", mono ? "mono" : null, v); kv.appendChild(dd); return dd; }
        row("k.cwd", j.cwd, true);
        var elapsed = row(isEnded(j) ? "k.ran" : "k.el", fmtEl((isEnded(j) ? j.endedAt : now()) - j.startedAt));
        if (!isEnded(j)) elapsed.setAttribute("data-since", j.startedAt);
        row("k.started", fmtClock(j.startedAt));
        row("k.owner", j.owner === "user" ? t("own.user.long") : t("own.agent.long") + " · " + t("pane.lead"));
        tipEl.appendChild(kv);
        var s = stOf(j), note = "";
        if (s === "lost") note = t("tt.note.lost"); else if (s === "nostart") note = t("tt.note.nostart");
        else if (s === "stopped" && j.stoppedBy === "user") note = j.owner === "user" ? t("tt.note.stopped.user.nonotice") : (j.notice && (j.notice.state === "steered" || j.notice.state === "woken") ? t("tt.note.stopped.user") : j.notice && j.notice.state === "sending" ? t("nt.sending") : j.notice ? t("nt." + j.notice.state) : "");
        if (note) tipEl.appendChild(el("div", "tt-note", note));
        tipEl.appendChild(el("div", "tt-hint", j.owner === "user" ? t("tt.hint.user") : t("tt.hint.run")));
      } else {
        var list = foldedJobs; var head2 = el("div", "tt-head");
        head2.appendChild(el("span", "tt-title", kind === "agg" ? t("tt.agg.title", list.length) : t("tt.more.title", list.length))); tipEl.appendChild(head2);
        var kv2 = el("dl", "tt-kv");
        list.slice(0, 6).forEach(function (j) { kv2.appendChild(el("dt", null, ownerLabel(j))); var dd = el("dd", "mono", cmdHead(j.cmd) + " · " + (isEnded(j) ? shortText(j) : fmtEl(now() - j.startedAt))); kv2.appendChild(dd); });
        tipEl.appendChild(kv2);
        tipEl.appendChild(el("div", "tt-hint", t("tt.hint.run")));
      }
      tipEl.hidden = false;
      var a = (anchor || group).getBoundingClientRect(), c = cw.getBoundingClientRect(), p = pane.getBoundingClientRect(), tr = tipEl.getBoundingClientRect();
      var left = a.left - c.left + a.width / 2 - tr.width / 2;
      left = Math.max(p.left - c.left + 8, Math.min(left, p.right - c.left - tr.width - 8));
      tipEl.style.left = Math.round(left) + "px"; tipEl.style.top = Math.round(a.top - c.top - tr.height - 10) + "px";
      if (anchor && anchor.setAttribute) { anchor.setAttribute("aria-describedby", tipEl.id); tipEl.setAttribute("data-anchor", anchor.getAttribute("data-id") || "kind:" + anchor.getAttribute("data-kind")); }
    }
    function hideTip() {
      S.tipId = ""; S.tipKind = ""; tipEl.hidden = true;
      Array.prototype.forEach.call(group.querySelectorAll("[aria-describedby]"), function (n) { n.removeAttribute("aria-describedby"); });
    }

    /* ----- peek panel ----- */
    var outEl = null, newBtn = null, footLive = null, footTail = null;
    function openPeek(id, origin) {
      S.peek = true; S.peekId = id; if (origin) S.origin = origin; S.confirm = ""; S.follow = true; S.pending = 0; hideTip(); renderPeek(); renderStrip();
    }
    function closePeek(restoreFocus) {
      var origin = S.origin; S.peek = false; S.confirm = ""; peekEl.hidden = true; renderStrip();
      if (restoreFocus && origin) { var b = group.querySelector('[data-id="' + origin + '"].xchip') || group.querySelector("[data-kind]:not([hidden])"); if (b) b.focus(); }
    }
    function peekJobs() { return S.jobs.filter(function (j) { return !j.dismissed || j.id === S.peekId; }).sort(function (a, b) { return a.seq - b.seq; }); }
    function renderPeek() {
      if (!S.peek) { peekEl.hidden = true; return; }
      var j = byId(S.peekId); if (!j) { closePeek(false); return; }
      peekEl.hidden = false; peekEl.textContent = ""; peekEl.setAttribute("aria-label", t("pk.label") + " — " + cmdHead(j.cmd));
      var top = el("div", "ptop");
      var tabs = el("div", "ptabs"); tabs.setAttribute("role", "tablist"); tabs.setAttribute("aria-label", t("pk.tabs"));
      peekJobs().forEach(function (x) {
        var tb = el("button", "ptab"); tb.type = "button"; tb.setAttribute("role", "tab"); tb.setAttribute("aria-selected", x.id === j.id ? "true" : "false"); tb.setAttribute("data-tab", x.id); tb.tabIndex = x.id === j.id ? 0 : -1;
        var d = el("span", "pdot"); d.setAttribute("data-st", stOf(x)); tb.appendChild(d); tb.appendChild(el("span", "t", cmdHead(x.cmd))); tabs.appendChild(tb);
      });
      top.appendChild(tabs);
      var pin = el("button", "ibtn"); pin.type = "button"; pin.setAttribute("data-act", "pin"); pin.setAttribute("aria-pressed", S.pinned ? "true" : "false"); pin.title = t("pk.pin"); pin.setAttribute("aria-label", t("pk.pin")); pin.textContent = "⌖"; top.appendChild(pin);
      var cl = el("button", "ibtn"); cl.type = "button"; cl.setAttribute("data-act", "close"); cl.title = t("pk.close"); cl.setAttribute("aria-label", t("pk.close")); cl.textContent = "×"; top.appendChild(cl);
      peekEl.appendChild(top);

      var meta = el("div", "pmeta");
      meta.appendChild(el("span", "pill " + tone(j), stateText(j)));
      meta.appendChild(el("span", "own", ownerLabel(j)));
      var elE = el("span", "el", (isEnded(j) ? t("k.ran") + " " + fmtEl(j.endedAt - j.startedAt) : fmtEl(now() - j.startedAt))); if (!isEnded(j)) elE.setAttribute("data-since", j.startedAt); meta.appendChild(elE);
      meta.appendChild(el("span", "grow"));
      if (j.owner === "user") { var jb = el("button", "btn"); jb.type = "button"; jb.setAttribute("data-act", "jump"); jb.textContent = "↑ " + t("pk.jump"); meta.appendChild(jb); }
      var cp = el("button", "btn"); cp.type = "button"; cp.setAttribute("data-act", "copy"); cp.textContent = t("pk.copy"); meta.appendChild(cp);
      if (!isEnded(j)) {
        var sbtn = el("button", "btn stop"); sbtn.type = "button"; sbtn.setAttribute("data-act", "stop");
        if (stOf(j) === "stopping") { sbtn.disabled = true; sbtn.innerHTML = '<span class="spinr"></span>'; sbtn.appendChild(document.createTextNode(t("stopping"))); }
        else { sbtn.innerHTML = "<i></i>"; sbtn.appendChild(document.createTextNode(t("stop"))); sbtn.setAttribute("aria-expanded", S.confirm === j.id ? "true" : "false"); }
        meta.appendChild(sbtn);
      }
      peekEl.appendChild(meta);

      var pc = el("div", "pcmd"); var code = el("code", "cl", j.cmd); code.title = t("pk.expand"); code.setAttribute("data-act", "cmd"); code.tabIndex = 0; code.setAttribute("role", "button"); code.setAttribute("aria-label", t("k.cmd") + ": " + j.cmd);
      if (S.cmdOpen) code.classList.add("open"); pc.appendChild(code); pc.appendChild(el("span", "cwd", "⌂ " + j.cwd)); peekEl.appendChild(pc);

      if (S.confirm === j.id && !isEnded(j)) {
        var cf = el("div", "xconf"); cf.setAttribute("role", "alertdialog"); cf.setAttribute("aria-label", t("conf.title"));
        cf.appendChild(el("b", null, t("conf.title"))); cf.appendChild(el("p", null, t("conf.body")));
        cf.appendChild(el("p", null, j.owner === "user" ? t("conf.user") : t("conf.agent", t("pane.lead"))));
        var rw = el("div", "row"); var tmo = el("span", "tmo"); tmo.setAttribute("data-conf-left", "1"); tmo.textContent = frozen ? "" : t("conf.auto", 8); rw.appendChild(tmo);
        var c1 = el("button", "btn"); c1.type = "button"; c1.setAttribute("data-act", "cancel"); c1.textContent = t("cancel"); rw.appendChild(c1);
        var c2 = el("button", "btn danger"); c2.type = "button"; c2.setAttribute("data-act", "confirm"); c2.textContent = t("stop.job"); rw.appendChild(c2);
        cf.appendChild(rw); peekEl.appendChild(cf);
      }
      if (j.stoppedBy === "user") peekEl.appendChild(noticeCard(j));

      var ow = el("div", "xoutwrap");
      outEl = el("pre", "xout" + (!isEnded(j) ? " live" : "") + (S.wrap ? "" : " nowrap")); outEl.setAttribute("role", "log"); outEl.setAttribute("aria-live", "off"); outEl.setAttribute("aria-label", t("out.label")); outEl.tabIndex = 0;
      fillOut(j); ow.appendChild(outEl);
      newBtn = el("button", "xnew"); newBtn.type = "button"; newBtn.hidden = true; newBtn.setAttribute("data-act", "follow"); ow.appendChild(newBtn);
      peekEl.appendChild(ow);
      var foot = el("div", "pfoot");
      footLive = el("span", "live" + (isEnded(j) ? " off" : "")); foot.appendChild(footLive);
      footTail = el("span"); foot.appendChild(footTail);
      var wr = el("button", "ibtn"); wr.type = "button"; wr.style.width = "auto"; wr.style.padding = "0 7px"; wr.style.fontSize = "11px"; wr.setAttribute("data-act", "wrap"); wr.setAttribute("aria-pressed", S.wrap ? "true" : "false"); wr.textContent = t("out.wrap"); wr.className += " sp"; foot.appendChild(wr);
      foot.appendChild(el("span", null, t("pk.foot")));
      peekEl.appendChild(foot);
      updateFoot(j);
      outEl.addEventListener("scroll", onOutScroll);
      if (S.follow) outEl.scrollTop = outEl.scrollHeight;
    }
    function fillOut(j) {
      outEl.textContent = "";
      if (!j.lines.length) { outEl.appendChild(el("span", "empty", t("out.empty"))); return; }
      var frag = document.createDocumentFragment();
      j.lines.slice(-120).forEach(function (l) { frag.appendChild(lineEl(l)); });
      outEl.appendChild(frag);
    }
    function lineEl(l) { var d = el("span", "l" + (l.s === "e" ? " e" : l.s === "sys" ? " sys" : ""), l.text); if (l.s === "sys") d.textContent = l.text; return d; }
    function updateFoot(j) {
      if (!footLive) return;
      footLive.className = "live" + (isEnded(j) ? " off" : S.follow ? "" : " off");
      footLive.textContent = isEnded(j) ? t("out.ended") : S.follow ? t("out.live") : t("out.paused");
      footTail.textContent = t("out.tail", fmtBytes(Math.min(j.bytes, 16384)), fmtBytes(j.bytes)) + " · stdout+stderr";
    }
    function onOutScroll() {
      var atEnd = outEl.scrollHeight - outEl.scrollTop - outEl.clientHeight < 10;
      S.follow = atEnd; if (atEnd) { S.pending = 0; if (newBtn) newBtn.hidden = true; }
      var j = byId(S.peekId); if (j) updateFoot(j);
    }
    function appendOut(j, lines) {
      if (!S.peek || S.peekId !== j.id || !outEl) return;
      var emp = outEl.querySelector(".empty"); if (emp) emp.remove();
      lines.forEach(function (l) { outEl.appendChild(lineEl(l)); });
      while (outEl.childNodes.length > 160) outEl.removeChild(outEl.firstChild);
      if (S.follow) outEl.scrollTop = outEl.scrollHeight;
      else { S.pending += lines.length; newBtn.hidden = false; newBtn.textContent = t("out.new", S.pending); }
      updateFoot(j);
    }
    function noticeCard(j) {
      var n = j.notice || { state: j.owner === "user" ? "none" : "sending" };
      var c = el("div", "xnotice"); c.setAttribute("data-ns", n.state); c.setAttribute("aria-live", "polite");
      var h = el("div", "nh"); h.appendChild(el("span", null, (n.state === "steered" || n.state === "woken" ? "✓ " : n.state === "sending" ? "… " : n.state === "none" ? "– " : "⚠ ") + t("nt." + n.state)));
      if (n.state === "failed") { var rb = el("button", "btn"); rb.type = "button"; rb.setAttribute("data-act", "retry"); rb.textContent = t("nt.retry"); h.appendChild(rb); }
      c.appendChild(h);
      if (n.state !== "none") {
        var d = el("details"); if (o.noticeOpen) d.open = true; d.appendChild(el("summary", null, t("nt.sample")));
        d.appendChild(el("pre", null, noticeText(j))); d.appendChild(el("div", "nl", t("nt.lang"))); c.appendChild(d);
      }
      return c;
    }
    function noticeText(j) {
      return '<ws-notice source="dashboard" kind="exec-stopped-by-user">\n' +
        "The user stopped exec job " + j.id + " from the dashboard\n(SIGKILL on its process group) at " + fmtClock(j.endedAt || now()) + ".\n" +
        "  command: " + (j.cmd.length > 70 ? j.cmd.slice(0, 67) + "..." : j.cmd) + "\n" +
        "  cwd:     " + j.cwd + "\n" +
        "  ran:     " + fmtEl((j.endedAt || now()) - j.startedAt) + "   output: " + fmtBytes(j.bytes) + " (exec.tail " + j.id + ")\n" +
        "This was deliberate. Do not relaunch it unless the user asks.\n</ws-notice>";
    }

    /* ----- stop flow ----- */
    var confTimer = 0, confLeft = 0;
    function askStop(id, origin) {
      var j = byId(id); if (!j || isEnded(j)) return;
      if (!S.peek || S.peekId !== id) { S.peek = true; S.peekId = id; S.follow = true; }
      if (origin) S.origin = origin; S.confirm = id; hideTip(); renderPeek(); renderStrip();
      var c = peekEl.querySelector('[data-act="cancel"]'); if (c && !frozen) c.focus();
      clearInterval(confTimer); confLeft = 8;
      if (!frozen) confTimer = setInterval(function () {
        confLeft--; var s = peekEl.querySelector("[data-conf-left]");
        if (confLeft <= 0) { clearInterval(confTimer); cancelStop(false); } else if (s) s.textContent = t("conf.auto", confLeft);
      }, 1000);
    }
    function cancelStop(focusBack) {
      clearInterval(confTimer); var id = S.confirm; S.confirm = ""; renderPeek();
      if (focusBack) { var b = peekEl.querySelector('[data-act="stop"]'); if (b) b.focus(); }
    }
    function doStop(id) {
      clearInterval(confTimer); var j = byId(id); if (!j) return;
      S.confirm = ""; j.state = "stopping"; renderPeek(); renderStrip();
      var f = peekEl.querySelector("[data-tab]"); var sel = peekEl.querySelector('[aria-selected="true"]'); if (sel && !frozen) sel.focus();
      later(function () {
        j.state = "stopped"; j.stoppedBy = "user"; j.endedAt = now(); pushLine(j, endLine(j));
        if (j.owner === "agent") {
          j.notice = { state: "sending" };
          later(function () {
            var st = S.agent === "working" ? "steered" : S.agent === "idle" ? "woken" : S.agent === "closed" ? "closed" : "failed";
            j.notice = { state: st }; afterNotice(j);
          }, 700);
        } else j.notice = { state: "none" };
        if (!(S.peek && S.peekId === j.id)) showToast(j);
        renderAll();
      }, 650);
    }
    function afterNotice(j) { renderAll(); if (S.toast && S.toast.id === j.id) showToast(j); }
    function showToast(j) {
      var ok = j.notice && (j.notice.state === "steered" || j.notice.state === "woken");
      var txt = j.owner === "user" ? t("toast.user", cmdHead(j.cmd)) : j.notice && j.notice.state === "sending" ? t("toast.agent", cmdHead(j.cmd)).replace(/·.*/, "· " + t("nt.sending")) : ok ? t("toast.agent", cmdHead(j.cmd)) : t("toast.agent.fail", cmdHead(j.cmd));
      S.toast = { id: j.id, text: txt };
      toastEl.hidden = false; toastEl.textContent = ""; toastEl.style.bottom = S.peek ? (peekEl.offsetHeight + 38) + "px" : ""; toastEl.appendChild(el("span", "tm")); toastEl.appendChild(el("span", null, txt));
      var vb = el("button", null, t("toast.view")); vb.type = "button"; vb.setAttribute("data-act", "toast-view"); toastEl.appendChild(vb);
      clearTimeout(S.toastT); if (!frozen) S.toastT = setTimeout(function () { toastEl.hidden = true; S.toast = null; }, 6500);
    }
    function retryNotice(id) {
      var j = byId(id); if (!j) return; j.notice = { state: "sending" }; renderAll();
      later(function () { j.notice = { state: S.agent === "closed" ? "closed" : "steered" }; if (S.agent === "down") S.agent = "working"; renderAll(); if (api.onAgentChange) api.onAgentChange(S.agent); }, 700);
    }

    /* ----- ticking, simulation ----- */
    function tick() {
      S.jobs.forEach(function (j) {
        if (j.state !== "running") return;
        var n = 1 + Math.floor(Math.random() * 3), ls = [];
        for (var i = 0; i < n; i++) { var l = genLine(j); pushLine(j, l); ls.push(l); }
        if (j.finishAfter && j.lines.length >= j.finishAfter) {
          j.state = "exited"; j.exit = j.finishExit; j.endedAt = now(); var el1 = endLine(j); pushLine(j, el1); ls.push(el1);
          appendOut(j, ls); renderAll(); later(function () { if (j.state === "exited" && j.exit === 0) { j.dismissed = !(S.peek && S.peekId === j.id); renderStrip(); } }, 12000); return;
        }
        appendOut(j, ls);
      });
    }
    function clockTick() {
      Array.prototype.forEach.call(host.querySelectorAll("[data-since]"), function (n) { n.textContent = fmtEl(now() - Number(n.getAttribute("data-since"))); });
      var sig = order().map(function (j) { return j.id; }).join(); if (sig !== lastSig) { lastSig = sig; renderStrip(); } else layoutStrip();
    }
    var simT = 0, clkT = 0, lastSig = "";
    if (!frozen) { simT = setInterval(tick, 850); clkT = setInterval(clockTick, 1000); }

    /* ----- events ----- */
    function chipOf(target) { return target.closest ? target.closest(".xchip, .xmore") : null; }
    function tipFor(c) {
      var kind = c.getAttribute("data-kind") || "job";
      if (kind === "job" && S.peek && S.peekId === c.getAttribute("data-id")) return;
      if (kind === "job") showTip("job", c.getAttribute("data-id"), c); else showTip(kind, "", c);
    }
    group.addEventListener("mouseover", function (e) { var c = chipOf(e.target); if (c) { c.classList.add("is-hovered"); tipFor(c); } });
    group.addEventListener("mouseout", function (e) { var c = chipOf(e.target); if (c && (!e.relatedTarget || !c.contains(e.relatedTarget))) { c.classList.remove("is-hovered"); hideTip(); } });
    group.addEventListener("focusin", function (e) { var c = chipOf(e.target); if (c) tipFor(c); });
    group.addEventListener("focusout", function (e) { if (!e.relatedTarget || !group.contains(e.relatedTarget)) hideTip(); });
    group.addEventListener("click", function (e) {
      var st = e.target.closest && e.target.closest(".xstop");
      if (st) { e.preventDefault(); var id = st.getAttribute("data-id"); if (st.getAttribute("data-act") === "dismiss") { var j = byId(id); j.dismissed = true; hideTip(); renderStrip(); } else askStop(id, id); return; }
      var c = chipOf(e.target); if (!c) return;
      var kind = c.getAttribute("data-kind");
      var id2 = kind ? (foldedJobs[0] || S.jobs[0] || {}).id : c.getAttribute("data-id");
      if (S.peek && S.peekId === id2 && !kind) { closePeek(false); return; }
      openPeek(id2, kind ? "" : id2);
    });
    group.addEventListener("keydown", function (e) {
      if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
      var btns = Array.prototype.filter.call(group.querySelectorAll(".xchip, .xmore"), function (b) { return !b.hidden && !b.closest("[hidden]"); });
      var i = btns.indexOf(document.activeElement); if (i < 0) return;
      e.preventDefault(); var nx = btns[(i + (e.key === "ArrowRight" ? 1 : -1) + btns.length) % btns.length]; nx.focus();
    });
    peekEl.addEventListener("click", function (e) {
      var tab = e.target.closest && e.target.closest("[data-tab]");
      if (tab) { S.peekId = tab.getAttribute("data-tab"); S.confirm = ""; S.follow = true; S.pending = 0; renderPeek(); renderStrip(); return; }
      var a = e.target.closest && e.target.closest("[data-act]"); if (!a) return;
      var act = a.getAttribute("data-act"), j = byId(S.peekId);
      if (act === "close") closePeek(true);
      else if (act === "pin") { S.pinned = !S.pinned; a.setAttribute("aria-pressed", S.pinned ? "true" : "false"); }
      else if (act === "stop") askStop(S.peekId);
      else if (act === "cancel") cancelStop(true);
      else if (act === "confirm") doStop(S.peekId);
      else if (act === "retry") retryNotice(S.peekId);
      else if (act === "cmd") { S.cmdOpen = !S.cmdOpen; a.classList.toggle("open", S.cmdOpen); }
      else if (act === "wrap") { S.wrap = !S.wrap; a.setAttribute("aria-pressed", S.wrap ? "true" : "false"); outEl.classList.toggle("nowrap", !S.wrap); }
      else if (act === "follow") { S.follow = true; S.pending = 0; newBtn.hidden = true; outEl.scrollTop = outEl.scrollHeight; updateFoot(j); }
      else if (act === "copy") { try { navigator.clipboard && navigator.clipboard.writeText(j.cmd); } catch (er) { } var old = a.textContent; a.textContent = "✓ " + t("pk.copied"); setTimeout(function () { a.textContent = old; }, 1100); }
      else if (act === "jump") {
        var card = msgs.querySelector('.cmdcard[data-job="' + j.id + '"]');
        if (!S.pinned) closePeek(false);
        if (card) { card.classList.remove("flash"); void card.offsetWidth; card.classList.add("flash"); card.scrollIntoView({ block: "nearest" }); }
      }
    });
    peekEl.addEventListener("keydown", function (e) {
      if (e.key === "ArrowLeft" || e.key === "ArrowRight") {
        var tab = e.target.closest && e.target.closest("[data-tab]"); if (!tab) return;
        var ts = Array.prototype.slice.call(peekEl.querySelectorAll("[data-tab]")), i = ts.indexOf(tab), nx = ts[(i + (e.key === "ArrowRight" ? 1 : -1) + ts.length) % ts.length];
        e.preventDefault(); S.peekId = nx.getAttribute("data-tab"); S.confirm = ""; renderPeek(); renderStrip(); var sel = peekEl.querySelector('[aria-selected="true"]'); if (sel) sel.focus();
      } else if ((e.key === "Enter" || e.key === " ") && e.target.getAttribute && e.target.getAttribute("data-act") === "cmd") { e.preventDefault(); S.cmdOpen = !S.cmdOpen; e.target.classList.toggle("open", S.cmdOpen); }
    });
    pane.addEventListener("keydown", function (e) {
      if (e.key !== "Escape") return;
      if (S.confirm) { e.preventDefault(); e.stopPropagation(); cancelStop(true); }
      else if (S.peek) { e.preventDefault(); closePeek(true); }
    });
    toastEl.addEventListener("click", function (e) { if (e.target.getAttribute("data-act") === "toast-view" && S.toast) { toastEl.hidden = true; openPeek(S.toast.id, S.toast.id); } });
    sb.addEventListener("click", function () { /* subagent popover lives in the product; inert here */ });
    function onDocClick(e) { if (!S.peek || S.pinned || frozen) return; if (!e.target.isConnected) return; /* the click re-rendered its own target */ if (pane.contains(e.target) && (peekEl.contains(e.target) || group.contains(e.target))) return; if (!pane.contains(e.target) || !cw.contains(e.target) || true) closePeek(false); }
    document.addEventListener("click", onDocClick);
    var ro = window.ResizeObserver ? new ResizeObserver(function () { layoutStrip(); }) : null; if (ro) ro.observe(cb);

    function renderAll() { renderChrome(); renderStrip(); if (S.peek) { var keep = S.follow; renderPeek(); } if (!tipEl.hidden && S.tipKind === "job") { /* keep tip text current */ } }

    /* ----- seeding ----- */
    function seed(name) {
      S.jobs = []; S.peek = false; S.confirm = ""; peekEl.hidden = true; toastEl.hidden = true; hideTip(); nameIdx = 0; var n = now();
      if (name === "one") addJob({ kind: "watch", startedAt: n - 134000 });
      else if (name === "many") {
        addJob({ kind: "watch", startedAt: n - 612000 }); addJob({ kind: "test", startedAt: n - 91000, finishAfter: 1e9 });
        addJob({ kind: "serve", startedAt: n - 305000 }); addJob({ kind: "make", startedAt: n - 47000 });
        addJob({ kind: "fetch", owner: "user", startedAt: n - 22000 }); addJob({ kind: "log", startedAt: n - 1840000 });
        addJob({ kind: "long", startedAt: n - 9000 });
      } else if (name === "states") {
        addJob({ kind: "watch", startedAt: n - 201000 });
        addJob({ kind: "make", state: "exited", exit: 0, startedAt: n - 88000, endedAt: n - 21000 });
        addJob({ kind: "test", state: "exited", exit: 2, startedAt: n - 130000, endedAt: n - 44000 });
        addJob({ kind: "serve", state: "stopped", stoppedBy: "user", startedAt: n - 410000, endedAt: n - 63000, notice: { state: "steered" } });
        addJob({ kind: "log", state: "lost", startedAt: n - 3200000, endedAt: n - 1800000 });
        addJob({ kind: "fetch", owner: "user", state: "failed", startedAt: n - 5000, endedAt: n - 5000, preLines: 0 });
        S.jobs[S.jobs.length - 1].state = "nostart";
      }
      renderAll();
    }
    function cfg(c) {
      if (c.lang) S.lang = c.lang;
      if (c.narrow != null) { S.narrow = c.narrow; pane.classList.toggle("narrow", S.narrow); if (o.resizable) pane.style.setProperty("--pane-w", (S.narrow ? 360 : (o.paneW || 780)) + "px"); }
      if (c.agent) S.agent = c.agent;
      if (c.subagents != null) S.subagents = c.subagents;
      if (c.bg != null) S.bg = c.bg;
      renderAll();
    }
    function destroy() { clearInterval(simT); clearInterval(clkT); clearInterval(confTimer); timers.forEach(clearTimeout); document.removeEventListener("click", onDocClick); if (ro) ro.disconnect(); }

    var api = { S: S, seed: seed, cfg: cfg, addJob: function (spec) { var j = addJob(spec); renderAll(); return j; }, openPeek: openPeek, askStop: askStop, doStop: doStop, showTip: function (id) { var s = group.querySelector('.xchip[data-id="' + id + '"]'); if (s) showTip("job", id, s); }, showFolded: function (kind) { var b = group.querySelector('[data-kind="' + kind + '"]:not([hidden])'); if (b) showTip(kind, "", b); }, showToast: showToast, byId: byId, destroy: destroy, relayout: layoutStrip, renderAll: renderAll, onAgentChange: null };
    renderAll();
    if (o.scenario) seed(o.scenario);
    return api;
  }

  window.ExecHud = { create: create, I18N: I18N, NAMES: NAMES, CMD: CMD };
})();
