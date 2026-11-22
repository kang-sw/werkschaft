/* Frozen state gallery for the exec-job HUD mockup. Each cell is one
   ExecHud pane with a fixed clock, so the page renders identically every
   time. index.html shows it under "All states"; fragment.html is this DOM
   serialised for ko and en. */
(function () {
  "use strict";
  var T = new Date(2026, 10, 22, 21, 48, 3).getTime();
  var CAP = {
    en: {
      idle: ["1 · Idle — no jobs", "Nothing is drawn for exec; the strip keeps only the subagent badge."],
      one: ["2 · One job running", "Ring spins like a HUD tool node; label is the command head, then live elapsed. Corner ■ appears on hover."],
      many: ["3 · Many jobs (7) next to subagent + background badges", "Exec group folds from the end into +N; subagent badge keeps its slot."],
      tip: ["4 · Hover tooltip — agent job", "Full command, directory, elapsed, owner. Above the chip because the strip sits at the bottom."],
      tipu: ["5 · Hover tooltip — your ! job, long command", "Command clamps at 7 lines; the peek panel shows all of it. Hint points to the chat card."],
      peek: ["6 · Live output peek (streaming)", "Opens upward over the conversation; tabs switch jobs; follows the tail unless you scroll up."],
      conf: ["7 · Stop confirmation", "Inline in the panel, focus on Cancel, reverts after 8 s. Says exactly what is killed and who is told."],
      toast: ["8 · Stopped by you — toast (panel closed)", "The chip turns to the stopped state; the toast confirms the agent was told and offers View."],
      stopped: ["9 · Stopped by you — notice card in the panel", "Card states how the notice landed and holds the exact text the agent receives."],
      ended: ["10 · Ended states", "Exited ok · failed (exit 2) · stopped by you · lost · never started. Ended chips show the outcome instead of elapsed; × dismisses."],
      narrow: ["11 · Very narrow (320 px)", "No room for even one chip: a single aggregate chip (count + worst state). At 360 px one chip + +N still fits."],
      narrowpk: ["12 · Narrow peek", "Panel takes the full pane width; tabs scroll horizontally."],
      userstop: ["13 · Stopping your own ! job", "No agent notice (policy question); the confirm says so."]
    },
    ko: {
      idle: ["1 · 유휴 — 작업 없음", "exec는 아무것도 그리지 않고, strip에는 서브에이전트 배지만 남습니다."],
      one: ["2 · 작업 1개 실행 중", "링은 HUD tool 노드처럼 회전하고, 라벨은 명령 앞부분 + 실시간 경과. 호버 시 모서리 ■ 표시."],
      many: ["3 · 여러 작업(7) + 서브에이전트·백그라운드 배지", "exec 그룹은 뒤에서부터 +N으로 접히고 서브에이전트 배지 자리는 유지됩니다."],
      tip: ["4 · 호버 툴팁 — 에이전트 작업", "전체 명령, 디렉터리, 경과, 실행 주체. strip이 하단이라 칩 위쪽에 뜹니다."],
      tipu: ["5 · 호버 툴팁 — 내 ! 작업, 긴 명령", "명령은 7줄에서 잘리고 전체는 출력 패널에서 봅니다. 힌트가 채팅 카드를 안내."],
      peek: ["6 · 실시간 출력 peek (스트리밍)", "대화 위로 열림. 탭으로 작업 전환, 위로 스크롤하지 않는 한 끝을 따라감."],
      conf: ["7 · 중지 확인", "패널 안 인라인, 포커스는 취소에, 8초 뒤 되돌아감. 무엇이 종료되고 누구에게 알리는지 명시."],
      toast: ["8 · 사용자가 중지 — 토스트 (패널 닫힘)", "칩이 중지 상태로 바뀌고, 토스트가 에이전트 알림 전달을 확인하며 보기를 제공합니다."],
      stopped: ["9 · 사용자가 중지 — 패널의 알림 카드", "알림이 어떻게 도달했는지와 에이전트가 받는 알림 원문을 담은 카드."],
      ended: ["10 · 종료 상태들", "정상 종료 · 실패(코드 2) · 사용자 중지 · 유실 · 시작 안 됨. 종료 칩은 경과 대신 결과 표시, × 로 닫기."],
      narrow: ["11 · 매우 좁은 폭 (320px)", "칩 하나 들어갈 자리도 없으면 집계 칩 하나(개수 + 가장 나쁜 상태). 360px에서는 칩 1개 + +N까지 들어갑니다."],
      narrowpk: ["12 · 좁은 폭 peek", "패널이 pane 전체 폭을 쓰고 탭은 가로 스크롤."],
      userstop: ["13 · 내 ! 작업 중지", "에이전트 알림 없음(정책 질문). 확인 문구에 그렇게 명시."]
    }
  };

  var CELLS = [
    { key: "idle", opts: { scenario: "idle", agent: "idle", paneW: 600 } },
    { key: "one", opts: { scenario: "one", paneW: 600 } },
    { key: "many", opts: { scenario: "many", paneW: 600, bg: 2 } },
    { key: "tip", opts: { scenario: "one", paneW: 600 }, setup: function (h) { h.showTip(h.S.jobs[0].id); } },
    { key: "tipu", opts: { paneW: 600, tall: true }, setup: function (h) { h.addJob({ kind: "long", owner: "user", startedAt: T - 9000 }); h.showTip(h.S.jobs[0].id); } },
    { key: "peek", opts: { scenario: "many", tall: true, paneW: 600 }, setup: function (h) { h.openPeek(h.S.jobs[1].id, h.S.jobs[1].id); } },
    { key: "conf", opts: { scenario: "many", tall: true, paneW: 600 }, setup: function (h) { h.askStop(h.S.jobs[0].id, h.S.jobs[0].id); } },
    { key: "toast", opts: { scenario: "states", paneW: 600 }, setup: function (h) { var j = h.S.jobs.filter(function (x) { return x.stoppedBy === "user"; })[0]; j.owner = "agent"; h.showToast(j); } },
    { key: "stopped", opts: { scenario: "states", tall: true, paneW: 600, paneH: 640, noticeOpen: true }, setup: function (h) { var j = h.S.jobs.filter(function (x) { return x.stoppedBy === "user"; })[0]; h.openPeek(j.id, j.id); } },
    { key: "ended", wide: true, opts: { scenario: "states", paneW: 780 } },
    { key: "narrow", opts: { scenario: "many", narrow: true, tall: true, paneW: 320 } },
    { key: "narrowpk", opts: { scenario: "many", narrow: true, tall: true, paneW: 360 }, setup: function (h) { h.openPeek(h.S.jobs[1].id, h.S.jobs[1].id); } },
    { key: "userstop", opts: { tall: true, paneW: 600 }, setup: function (h) { var j = h.addJob({ kind: "dev", owner: "user", startedAt: T - 75000 }); h.askStop(j.id, j.id); } }
  ];

  function build(root, lang) {
    root.textContent = "";
    CELLS.forEach(function (c) {
      var cell = document.createElement("section"); cell.className = "cell frozen" + (c.wide ? " wide" : ""); cell.setAttribute("data-cell", c.key);
      var cap = CAP[lang][c.key];
      var h3 = document.createElement("h3"); h3.textContent = cap[0]; cell.appendChild(h3);
      var host = document.createElement("div"); cell.appendChild(host);
      var note = document.createElement("div"); note.className = "noteline"; note.textContent = cap[1]; cell.appendChild(note);
      root.appendChild(cell);
      var o = { frozen: true, now: T, lang: lang };
      for (var k in c.opts) o[k] = c.opts[k];
      var h = window.ExecHud.create(host, o);
      if (c.setup) c.setup(h);
      h.relayout();
    });
    // fonts can shift widths: fold once more after they settle
    if (document.fonts && document.fonts.ready) document.fonts.ready.then(function () {
      Array.prototype.forEach.call(root.querySelectorAll(".cell"), function () { });
    });
  }
  window.ExecHudGallery = { build: build, CELLS: CELLS };
})();
