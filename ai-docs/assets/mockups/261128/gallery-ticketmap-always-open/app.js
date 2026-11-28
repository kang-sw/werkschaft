(() => {
  'use strict';

  /* ==========================================================================
     Realistic Werkschaft Ticket Dataset (12 Tickets across 4 Clusters)
     ========================================================================== */
  const CLUSTERS = [
    { id: 'shell', name: 'Shell & Gallery', hue: 215 },
    { id: 'companion', name: 'Companion & Voice', hue: 280 },
    { id: 'metrics', name: 'Werk Usage & Quota', hue: 155 },
    { id: 'conv', name: 'Conv History API', hue: 32 },
  ];

  const STATUS_META = [
    { id: 'ready', label: 'ready', sym: null },
    { id: 'todo', label: 'todo', sym: null },
    { id: 'idea', label: 'idea', sym: null },
    { id: 'done', label: 'done', sym: '✓' },
    { id: 'dropped', label: 'dropped', sym: '×' },
  ];

  const TICKETS = [
    {
      stem: '261126-epic-dashboard-shell-overhaul',
      title: '대시보드 셸 오버홀 (Variant D + 상시 도킹 갤러리)',
      status: 'todo',
      category: 'epic',
      cluster: 'shell',
      phasesDone: 2,
      phasesTotal: 4,
      blocked: false,
      odq: false,
      assignee: 'kang-sw',
      lines: 312,
      x: 190,
      y: 118,
      summary: 'Variant D 외부 프레임, 상시 열린 #bgal 고정 시스템 페이지(티켓 맵·Git·Workpanel), 하단 전역 바를 통합하는 에픽.',
      links: [
        { type: 'child', target: '261127-feat-variant-d-outer-chrome' },
        { type: 'child', target: '261128-feat-gallery-always-open-ticketmap' },
        { type: 'child', target: '261128-feat-gallery-git-page' },
        { type: 'child', target: '261128-research-gallery-workpanel-design' },
      ],
      similar: [
        { target: '261124-feat-companion-layered-agents', pct: 68 },
      ],
    },
    {
      stem: '261127-feat-variant-d-outer-chrome',
      title: 'Track 1: Variant D 상단 세션 바 · 하단 전역 바 구현',
      status: 'done',
      category: 'feat',
      cluster: 'shell',
      phasesDone: 3,
      phasesTotal: 3,
      blocked: false,
      odq: false,
      assignee: 'kang-sw',
      lines: 198,
      x: 105,
      y: 205,
      summary: '36px 3열 상단 바, 12px 라운드 스테이지 웰, 26px 하단 전역 바 및 좌하단 #rail-companion-slot 확정.',
      links: [
        { type: 'parent', target: '261126-epic-dashboard-shell-overhaul' },
        { type: 'related', target: '261124-feat-companion-layered-agents' },
      ],
      similar: [
        { target: '261128-feat-gallery-always-open-ticketmap', pct: 74 },
      ],
    },
    {
      stem: '261128-feat-gallery-always-open-ticketmap',
      title: 'Track 2A: 항상 열린 #bgal + 도킹 티켓 맵',
      status: 'ready',
      category: 'feat',
      cluster: 'shell',
      phasesDone: 1,
      phasesTotal: 3,
      blocked: false,
      odq: false,
      assignee: 'kang-sw',
      lines: 245,
      x: 245,
      y: 222,
      summary: '#thandle 은퇴 후 #bgal 내부에 티켓 맵을 기본 고정 페이지로 상주시키고, @container 툴바·바텀 시트 인스펙터·#tmback 전체화면 확장을 제공.',
      links: [
        { type: 'parent', target: '261126-epic-dashboard-shell-overhaul' },
        { type: 'blocks', target: '261128-feat-gallery-git-page' },
        { type: 'related', target: '261128-research-gallery-workpanel-design' },
      ],
      similar: [
        { target: '261128-feat-gallery-git-page', pct: 82 },
        { target: '261127-feat-variant-d-outer-chrome', pct: 74 },
      ],
    },
    {
      stem: '261128-feat-gallery-git-page',
      title: 'Track 2B: #bgal 고정 Git 페이지 (Graph · Diff · Worktrees)',
      status: 'todo',
      category: 'feat',
      cluster: 'shell',
      phasesDone: 0,
      phasesTotal: 2,
      blocked: false,
      odq: false,
      assignee: 'kang-sw',
      lines: 164,
      x: 342,
      y: 165,
      summary: '레거시 #ghandle 드로어를 대체할 3-영역 Git 도킹 페이지 구현 (커밋 그래프, 파일 Diff, 워크트리 관리).',
      links: [
        { type: 'parent', target: '261126-epic-dashboard-shell-overhaul' },
        { type: 'blocked-by', target: '261128-feat-gallery-always-open-ticketmap' },
      ],
      similar: [
        { target: '261128-feat-gallery-always-open-ticketmap', pct: 82 },
      ],
    },
    {
      stem: '261128-research-gallery-workpanel-design',
      title: 'Track 2C: Workpanel 통합 설계 (todo/agenda/notes/manuals)',
      status: 'idea',
      category: 'research',
      cluster: 'shell',
      phasesDone: 0,
      phasesTotal: 0,
      blocked: false,
      odq: true,
      assignee: 'unassigned',
      lines: 128,
      x: 315,
      y: 295,
      summary: '레거시 #drhandle 드로어의 todo, agenda, notes, manuals를 단일 Workpanel 고정 시스템 페이지로 통합하는 구조 연구.',
      links: [
        { type: 'parent', target: '261126-epic-dashboard-shell-overhaul' },
        { type: 'related', target: '261128-feat-gallery-always-open-ticketmap' },
      ],
      similar: [
        { target: '261128-feat-gallery-always-open-ticketmap', pct: 71 },
      ],
    },
    {
      stem: '261124-feat-companion-layered-agents',
      title: '컴패니언 레이어드 에이전트 & #rail-companion-slot',
      status: 'ready',
      category: 'feat',
      cluster: 'companion',
      phasesDone: 2,
      phasesTotal: 3,
      blocked: false,
      odq: false,
      assignee: 'kang-sw',
      lines: 276,
      x: 560,
      y: 132,
      summary: '좌측 레일 하단 76px 고정 슬롯에 2D 컴패니언 스테이지를 결합하고, 레이어드 에이전트 이벤트와 발화 버블을 연동.',
      links: [
        { type: 'related', target: '261127-feat-variant-d-outer-chrome' },
        { type: 'blocks', target: '261125-feat-voice-stt-pipeline' },
      ],
      similar: [
        { target: '261125-feat-voice-stt-pipeline', pct: 79 },
        { target: '261126-epic-dashboard-shell-overhaul', pct: 68 },
      ],
    },
    {
      stem: '261125-feat-voice-stt-pipeline',
      title: '음성 STT 파이프라인 & 컴패니언 실시간 발화',
      status: 'todo',
      category: 'feat',
      cluster: 'companion',
      phasesDone: 1,
      phasesTotal: 3,
      blocked: true,
      odq: false,
      assignee: 'kang-sw',
      lines: 210,
      x: 685,
      y: 185,
      summary: '로컬 마이크 스트림 기반 실시간 STT 전사 및 컴패니언 메신저 입력 파이프라인 구축.',
      links: [
        { type: 'blocked-by', target: '261124-feat-companion-layered-agents' },
      ],
      similar: [
        { target: '261124-feat-companion-layered-agents', pct: 79 },
      ],
    },
    {
      stem: '261122-idea-companion-emotion-rig',
      title: '2D 컴패니언 표정 리깅 & 세션 페이즈 연동',
      status: 'idea',
      category: 'feat',
      cluster: 'companion',
      phasesDone: 0,
      phasesTotal: 2,
      blocked: false,
      odq: true,
      assignee: 'unassigned',
      lines: 94,
      x: 618,
      y: 268,
      summary: '에이전트 thinking/tool/permission 상태에 따라 SVG 컴패니언 표정과 모션을 부드럽게 전환.',
      links: [
        { type: 'related', target: '261124-feat-companion-layered-agents' },
      ],
      similar: [
        { target: '261124-feat-companion-layered-agents', pct: 66 },
      ],
    },
    {
      stem: '261125-feat-werk-usage-metrics',
      title: 'Werk 5h/주간 사용량 메트릭 & 계정 슬롯 원클릭 전환',
      status: 'ready',
      category: 'feat',
      cluster: 'metrics',
      phasesDone: 2,
      phasesTotal: 2,
      blocked: false,
      odq: false,
      assignee: 'kang-sw',
      lines: 184,
      x: 168,
      y: 410,
      summary: '하단 우측 .usage-toggle에 Claude·Codex·Werk 5h/주간 잔여량과 상향 팝오버 슬롯 스위처를 통합.',
      links: [
        { type: 'related', target: '261120-chore-legacy-quota-banner' },
      ],
      similar: [
        { target: '261127-feat-variant-d-outer-chrome', pct: 61 },
      ],
    },
    {
      stem: '261120-chore-legacy-quota-banner',
      title: '구형 상단 쿼터 배너 유지안 (하단 .usage-toggle 대체)',
      status: 'dropped',
      category: 'chore',
      cluster: 'metrics',
      phasesDone: 0,
      phasesTotal: 1,
      blocked: false,
      odq: false,
      assignee: 'kang-sw',
      lines: 62,
      x: 295,
      y: 446,
      summary: 'Variant D 하단 전역 상태 바 확정으로 폐기된 상단 쿼터 배너 유지안.',
      links: [
        { type: 'related', target: '261125-feat-werk-usage-metrics' },
      ],
      similar: [],
    },
    {
      stem: '261126-feat-conv-history-api',
      title: '대화 히스토리 페이징 API & 증분 스냅샷 캐시',
      status: 'ready',
      category: 'feat',
      cluster: 'conv',
      phasesDone: 1,
      phasesTotal: 2,
      blocked: false,
      odq: false,
      assignee: 'kang-sw',
      lines: 232,
      x: 540,
      y: 412,
      summary: '대규모 세션 대화를 커서 기반으로 분할 로드하고 메모리 증분 캐시로 파일 I/O를 최소화.',
      links: [
        { type: 'blocks', target: '261127-bug-conv-scroll-anchor-jump' },
      ],
      similar: [
        { target: '261127-bug-conv-scroll-anchor-jump', pct: 76 },
      ],
    },
    {
      stem: '261127-bug-conv-scroll-anchor-jump',
      title: '세션 전환 시 대화 스트림 스크롤 앵커 튐 현상 수정',
      status: 'todo',
      category: 'bug',
      cluster: 'conv',
      phasesDone: 0,
      phasesTotal: 1,
      blocked: false,
      odq: false,
      assignee: 'kang-sw',
      lines: 88,
      x: 668,
      y: 432,
      summary: '이전 페이지 로드 직후 overflow-anchor 충돌로 대화창 하단 고정이 풀리는 회귀 버그 수정.',
      links: [
        { type: 'blocked-by', target: '261126-feat-conv-history-api' },
        { type: 'related', target: '261126-epic-dashboard-shell-overhaul' },
      ],
      similar: [
        { target: '261126-feat-conv-history-api', pct: 76 },
      ],
    },
  ];

  const CLUSTER_MAP = Object.fromEntries(CLUSTERS.map((c) => [c.id, c]));
  const TICKET_MAP = Object.fromEntries(TICKETS.map((t) => [t.stem, t]));

  /* ==========================================================================
     Shared Reactive State (Synced between Docked #bgal & Fullscreen #tmback)
     ========================================================================== */
  const state = {
    preset: 'wide-tm',
    prevPreset: 'wide-tm',
    theme: 'dark',
    bgalWidth: 540,
    activePage: 'ticketmap',
    tmView: 'c', // 'b' | 'c'
    selectedStem: '261128-feat-gallery-always-open-ticketmap',
    sheetOpen: true,
    sheetCollapsed: false,
    fullInspMin: false,
    query: '',
    statuses: new Set(['ready', 'todo', 'idea', 'done', 'dropped']),
    clusters: new Set(CLUSTERS.map((c) => c.id)),
    edges: { parent: true, blocked: true, related: true, sim: true },
    zoom: { dock: 1, full: 1 },
    pan: { dock: { x: 0, y: 0 }, full: { x: 0, y: 0 } },
    tmFocused: false,
  };

  /* ==========================================================================
     DOM References
     ========================================================================== */
  const root = document.documentElement;
  const bgal = document.getElementById('bgal');
  const bgalEdge = document.getElementById('bgal-edge');
  const galRail = document.getElementById('gal-rail');
  const galPop = document.getElementById('gal-pop');
  const tmDocked = document.getElementById('tm-docked');
  const tmDockSheet = document.getElementById('tm-dock-sheet');
  const tmSheetStem = document.getElementById('tm-sheet-stem');
  const tmDockInspBody = document.getElementById('tm-dock-insp-body');
  const tmBack = document.getElementById('tmback');
  const tmFullInsp = document.getElementById('tm-full-insp');
  const tmFullInspBody = document.getElementById('tm-full-insp-body');
  const tmFullStats = document.getElementById('tm-full-stats');
  const tmFullModal = document.getElementById('tm-full-modal');
  const tmCtip = document.getElementById('tm-ctip');
  const miniToast = document.getElementById('mini-toast');
  const mBgalWidth = document.getElementById('m-bgal-width');
  const mSheetToggle = document.getElementById('m-sheet-toggle');
  const mSheetLbl = document.getElementById('m-sheet-lbl');

  let toastTimer = 0;
  function showToast(msg) {
    miniToast.textContent = msg;
    miniToast.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => {
      miniToast.hidden = true;
    }, 2400);
  }

  function escapeHtml(str) {
    return String(str)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  /* ==========================================================================
     Preset & Layout Management
     ========================================================================== */
  function applyPreset(preset) {
    if (preset !== 'fullscreen') {
      state.prevPreset = preset;
    }
    state.preset = preset;
    root.dataset.preset = preset;

    document.querySelectorAll('.m-tab[data-preset]').forEach((btn) => {
      const on = btn.dataset.preset === preset;
      btn.classList.toggle('is-on', on);
      btn.setAttribute('aria-selected', String(on));
    });

    if (preset === 'fullscreen') {
      root.dataset.gal = 'col';
      tmBack.hidden = false;
      mBgalWidth.textContent = '#tmback · Fullscreen';
      renderAllTicketMap();
      return;
    }

    tmBack.hidden = true;

    if (preset === 'three-panel') {
      root.dataset.gal = 'narrow';
      setActivePage('main');
      mBgalWidth.textContent = '3-Panel · 1440px';
    } else if (preset === 'narrow-col') {
      root.dataset.gal = 'col';
      if (state.activePage === 'main') setActivePage('ticketmap');
      mBgalWidth.textContent = '#bgal · 380px (CQ)';
    } else {
      root.dataset.gal = 'col';
      root.style.setProperty('--bgal-w', `${state.bgalWidth}px`);
      if (state.activePage === 'main') setActivePage('ticketmap');
      mBgalWidth.textContent = `#bgal · ${state.bgalWidth}px`;
    }
    renderAllTicketMap();
  }

  function setActivePage(pageId) {
    if (root.dataset.gal === 'col' && pageId === 'main') {
      pageId = 'ticketmap';
    }
    state.activePage = pageId;
    bgal.dataset.activePage = pageId;
    root.dataset.narrowAway = String(root.dataset.gal === 'narrow' && pageId !== 'main');

    document.querySelectorAll('.gal-dot[data-page]').forEach((btn) => {
      const on = btn.dataset.page === pageId;
      btn.classList.toggle('active', on);
      btn.setAttribute('aria-selected', String(on));
    });

    document.querySelectorAll('.gal-page[data-page-panel]').forEach((panel) => {
      panel.classList.toggle('is-active', panel.dataset.pagePanel === pageId);
    });
  }

  /* ==========================================================================
     .gal-rail Interactive Hover Preview Popover (.gal-pop)
     ========================================================================== */
  function buildGalPopoverHtml(pageId) {
    if (pageId === 'main') {
      return {
        mode: 'pill',
        html: `<div class="gal-pop-pill"><span class="gal-pop-pill-glyph" style="color:#3fa853">⌂</span><span>메인 보드 (Page 0)</span></div>`,
      };
    }
    if (pageId === 'plus') {
      return {
        mode: 'pill',
        html: `<div class="gal-pop-pill"><span class="gal-pop-pill-glyph">+</span><span>새 사용자 페이지에 토픽 고정</span></div>`,
      };
    }
    if (pageId === 'ticketmap') {
      return {
        mode: 'page',
        html: `
          <div class="gal-pop-layout">
            <div class="gal-pop-card">
              <div class="gal-pop-card-hd">
                <span class="gal-pop-card-dot" style="background:var(--wb-primary)"></span>
                <span class="gal-pop-card-title">티켓 맵 (Ticket Map)</span>
                <span class="gal-pop-card-state">고정 시스템</span>
              </div>
              <div class="gal-pop-mini-map">
                <svg viewBox="0 0 220 92" width="100%" height="100%">
                  <ellipse cx="62" cy="44" rx="46" ry="30" fill="hsl(215 62% 60% / .16)" stroke="hsl(215 62% 60% / .4)" stroke-width="1"/>
                  <ellipse cx="162" cy="48" rx="42" ry="28" fill="hsl(280 62% 60% / .16)" stroke="hsl(280 62% 60% / .4)" stroke-width="1"/>
                  <line x1="48" y1="36" x2="78" y2="52" stroke="var(--wb-warn)" stroke-width="1.4"/>
                  <line x1="78" y1="52" x2="148" y2="42" stroke="var(--d-tm-sim)" stroke-width="1.2" stroke-dasharray="2 3"/>
                  <circle cx="48" cy="36" r="5" fill="hsl(215 62% 62%)" stroke="var(--wb-ok)" stroke-width="1.8"/>
                  <circle cx="78" cy="52" r="5" fill="hsl(215 62% 62%)"/>
                  <circle cx="148" cy="42" r="5" fill="hsl(280 62% 62%)" stroke="var(--wb-ok)" stroke-width="1.8"/>
                  <circle cx="176" cy="58" r="4.5" fill="hsl(280 62% 62%)"/>
                </svg>
              </div>
            </div>
          </div>`,
      };
    }
    if (pageId === 'git') {
      return {
        mode: 'page',
        html: `
          <div class="gal-pop-layout">
            <div class="gal-pop-card">
              <div class="gal-pop-card-hd">
                <span class="gal-pop-card-dot" style="background:var(--wb-warn)"></span>
                <span class="gal-pop-card-title">Git (3-Region)</span>
                <span class="gal-pop-card-state">Track 2B</span>
              </div>
              <div class="gal-pop-mini-grid">
                <div class="gal-pop-mini-cell">Graph</div>
                <div class="gal-pop-mini-cell">Diff</div>
                <div class="gal-pop-mini-cell" style="grid-column:1 / -1">Worktrees · develop (±14)</div>
              </div>
            </div>
          </div>`,
      };
    }
    if (pageId === 'workpanel') {
      return {
        mode: 'page',
        html: `
          <div class="gal-pop-layout">
            <div class="gal-pop-card">
              <div class="gal-pop-card-hd">
                <span class="gal-pop-card-dot" style="background:var(--wb-ok)"></span>
                <span class="gal-pop-card-title">Workpanel</span>
                <span class="gal-pop-card-state">Scaffold</span>
              </div>
              <div class="gal-pop-mini-grid">
                <div class="gal-pop-mini-cell">Todo</div>
                <div class="gal-pop-mini-cell">Agenda</div>
                <div class="gal-pop-mini-cell">Notes</div>
                <div class="gal-pop-mini-cell">Manuals</div>
              </div>
            </div>
          </div>`,
      };
    }
    // User pinned page p1 (2-pane vertical split)
    return {
      mode: 'page',
      html: `
        <div class="gal-pop-layout">
          <div class="gal-pop-split gal-pop-col">
            <div class="gal-pop-card">
              <div class="gal-pop-card-hd">
                <span class="gal-pop-card-dot" style="background:var(--gal-st-impl)"></span>
                <span class="gal-pop-card-title">Track 2A 도킹 갤러리 구현 체크</span>
                <span class="gal-pop-card-state">구현</span>
              </div>
              <div class="gal-pop-card-sub">고정 시스템 페이지와 사용자 핀 페이지의 공존 구조 검증</div>
            </div>
            <div class="gal-pop-card">
              <div class="gal-pop-card-hd">
                <span class="gal-pop-card-dot" style="background:var(--gal-st-discuss)"></span>
                <span class="gal-pop-card-title">380px 좁은 열 툴바 압축 논의</span>
                <span class="gal-pop-card-state">논의</span>
              </div>
              <div class="gal-pop-card-sub">@container 쿼리 기반 0px 오버플로 레이아웃 검증</div>
            </div>
          </div>
        </div>`,
    };
  }

  function showGalPop(dotBtn) {
    const pageId = dotBtn.dataset.page;
    const spec = buildGalPopoverHtml(pageId);
    galPop.dataset.mode = spec.mode;
    galPop.innerHTML = spec.html;

    const side = root.dataset.gal === 'narrow' ? 'right' : 'left';
    galPop.dataset.side = side;
    galPop.hidden = false;

    const dotRect = dotBtn.getBoundingClientRect();
    const popRect = galPop.getBoundingClientRect();
    const topRaw = dotRect.top + dotRect.height / 2 - popRect.height / 2;
    const top = Math.max(42, Math.min(window.innerHeight - popRect.height - 32, topRaw));
    const left = side === 'left'
      ? Math.max(8, dotRect.left - popRect.width - 10)
      : Math.min(window.innerWidth - popRect.width - 8, dotRect.right + 10);

    const caretY = Math.max(14, Math.min(popRect.height - 14, dotRect.top + dotRect.height / 2 - top));
    galPop.style.setProperty('--caret-y', `${caretY}px`);
    galPop.style.transform = `translate3d(${Math.round(left)}px, ${Math.round(top)}px, 0)`;
  }

  function hideGalPop() {
    galPop.hidden = true;
  }

  /* ==========================================================================
     Ticket Filtering & Data Helpers
     ========================================================================== */
  function matchesQuery(ticket, q) {
    if (!q) return true;
    const needle = q.toLowerCase();
    const cl = CLUSTER_MAP[ticket.cluster];
    return (
      ticket.title.toLowerCase().includes(needle) ||
      ticket.stem.toLowerCase().includes(needle) ||
      ticket.category.toLowerCase().includes(needle) ||
      (cl && cl.name.toLowerCase().includes(needle))
    );
  }

  function getVisibleTickets() {
    return TICKETS.filter((t) => state.statuses.has(t.status) && state.clusters.has(t.cluster));
  }

  function getNeighbors(stem) {
    const set = new Set();
    const t = TICKET_MAP[stem];
    if (!t) return set;
    t.links.forEach((l) => set.add(l.target));
    t.similar.forEach((s) => set.add(s.target));
    TICKETS.forEach((other) => {
      if (other.links.some((l) => l.target === stem) || other.similar.some((s) => s.target === stem)) {
        set.add(other.stem);
      }
    });
    return set;
  }

  /* ==========================================================================
     Render Status Pills & Cluster Popovers (Synced across Dock & Fullscreen)
     ========================================================================== */
  function renderStatusPills(containerId, showCounts) {
    const el = document.getElementById(containerId);
    if (!el) return;
    el.innerHTML = STATUS_META.map((st) => {
      const pressed = state.statuses.has(st.id);
      const count = TICKETS.filter((t) => t.status === st.id && state.clusters.has(t.cluster)).length;
      const dotHtml = st.sym
        ? `<span class="d-tm-psym">${st.sym}</span>`
        : `<span class="d-tm-pdot"></span>`;
      const countHtml = showCounts ? `<span class="d-tm-n">${count}</span>` : `<span class="d-tm-n">${count}</span>`;
      return `
        <button type="button" class="d-tm-pill d-tm-st-${st.id}" data-status-toggle="${st.id}" aria-pressed="${pressed}" title="${st.label} (${count})">
          ${dotHtml}
          <span class="d-tm-pl">${st.label}</span>
          ${countHtml}
        </button>`;
    }).join('');
  }

  function renderClusterPopover(popId, cntId) {
    const pop = document.getElementById(popId);
    const cnt = document.getElementById(cntId);
    if (cnt) cnt.textContent = String(state.clusters.size);
    if (!pop) return;
    const allChecked = state.clusters.size === CLUSTERS.length;
    pop.innerHTML = `
      <label class="d-tm-clrow" style="border-bottom:1px solid var(--wb-line);margin-bottom:4px;">
        <input type="checkbox" data-cl-all ${allChecked ? 'checked' : ''} />
        <span class="d-tm-cll"><strong>모든 클러스터</strong></span>
        <span class="d-tm-n">${TICKETS.length}</span>
      </label>
      ${CLUSTERS.map((cl) => {
        const checked = state.clusters.has(cl.id);
        const n = TICKETS.filter((t) => t.cluster === cl.id).length;
        return `
          <label class="d-tm-clrow">
            <input type="checkbox" data-cl-id="${cl.id}" ${checked ? 'checked' : ''} />
            <span class="d-tm-cdot" style="--d-tm-h:${cl.hue}"></span>
            <span class="d-tm-cll">${escapeHtml(cl.name)}</span>
            <span class="d-tm-n">${n}</span>
          </label>`;
      }).join('')}
    `;
  }

  /* ==========================================================================
     Render View C: Interactive SVG Constellation Graph
     ========================================================================== */
  function renderViewC(containerId, mode) {
    const el = document.getElementById(containerId);
    if (!el) return;

    const visible = getVisibleTickets();
    const visibleSet = new Set(visible.map((t) => t.stem));
    const q = state.query.trim();
    const hasQ = q.length > 0;
    const hasSel = Boolean(state.selectedStem && visibleSet.has(state.selectedStem));
    const neighbors = hasSel ? getNeighbors(state.selectedStem) : new Set();

    // Build deduplicated edge list
    const edgeList = [];
    const seenEdges = new Set();
    visible.forEach((t) => {
      t.links.forEach((l) => {
        if (!visibleSet.has(l.target)) return;
        let kind = 'related';
        let from = t;
        let to = TICKET_MAP[l.target];
        if (l.type === 'parent') {
          kind = 'parent';
        } else if (l.type === 'child') {
          kind = 'parent';
          from = TICKET_MAP[l.target];
          to = t;
        } else if (l.type === 'blocked-by') {
          kind = 'blocked';
        } else if (l.type === 'blocks') {
          kind = 'blocked';
          from = TICKET_MAP[l.target];
          to = t;
        }
        const key = `${kind}:${from.stem}->${to.stem}`;
        const revKey = `${kind}:${to.stem}->${from.stem}`;
        if (seenEdges.has(key) || (kind === 'related' && seenEdges.has(revKey))) return;
        seenEdges.add(key);
        edgeList.push({ kind, from, to });
      });

      t.similar.forEach((s) => {
        if (!visibleSet.has(s.target)) return;
        const pair = [t.stem, s.target].sort().join('~');
        const key = `sim:${pair}`;
        if (seenEdges.has(key)) return;
        seenEdges.add(key);
        edgeList.push({ kind: 'sim', from: t, to: TICKET_MAP[s.target], pct: s.pct });
      });
    });

    // Cluster Hulls (soft rounded bounding blobs around each cluster's visible nodes)
    const hullsSvg = CLUSTERS.map((cl) => {
      const members = visible.filter((t) => t.cluster === cl.id);
      if (!members.length) return '';
      const xs = members.map((m) => m.x);
      const ys = members.map((m) => m.y);
      const minX = Math.min(...xs) - 58;
      const maxX = Math.max(...xs) + 58;
      const minY = Math.min(...ys) - 44;
      const maxY = Math.max(...ys) + 46;
      return `
        <g style="--d-tm-h:${cl.hue}">
          <rect class="d-tm-chull" x="${minX}" y="${minY}" width="${maxX - minX}" height="${maxY - minY}" rx="34" />
          <text class="d-tm-cl-label" x="${minX + 16}" y="${minY + 18}">${escapeHtml(cl.name)} (${members.length})</text>
        </g>`;
    }).join('');

    // Edges SVG
    const markerPrefix = `tm-mk-${mode}`;
    const edgesSvg = edgeList.map((e) => {
      if (!state.edges[e.kind]) return '';
      const hl = hasSel && (e.from.stem === state.selectedStem || e.to.stem === state.selectedStem);
      const dx = e.to.x - e.from.x;
      const dy = e.to.y - e.from.y;
      const dist = Math.hypot(dx, dy) || 1;
      const pad = 16;
      const x1 = e.from.x + (dx / dist) * pad;
      const y1 = e.from.y + (dy / dist) * pad;
      const x2 = e.to.x - (dx / dist) * pad;
      const y2 = e.to.y - (dy / dist) * pad;
      const cx = (x1 + x2) / 2 - (dy / dist) * (e.kind === 'sim' ? 18 : 10);
      const cy = (y1 + y2) / 2 + (dx / dist) * (e.kind === 'sim' ? 18 : 10);
      const marker = e.kind === 'blocked'
        ? `marker-end="url(#${markerPrefix}-blk)"`
        : e.kind === 'parent'
          ? `marker-end="url(#${markerPrefix}-par)"`
          : '';
      return `<path class="d-tm-ced d-tm-ce-${e.kind}${hl ? ' d-tm-hl' : ''}" d="M${x1.toFixed(1)},${y1.toFixed(1)} Q${cx.toFixed(1)},${cy.toFixed(1)} ${x2.toFixed(1)},${y2.toFixed(1)}" ${marker} />`;
    }).join('');

    // Nodes SVG
    const nodesSvg = visible.map((t) => {
      const cl = CLUSTER_MAP[t.cluster];
      const isSel = t.stem === state.selectedStem;
      const isNb = neighbors.has(t.stem);
      const isHit = hasQ && matchesQuery(t, q);
      const radius = t.category === 'epic' ? 14 : 11;
      const cls = [
        'd-tm-cnd',
        `d-tm-st-${t.status}`,
        isSel ? 'd-tm-sel' : '',
        isNb ? 'd-tm-nb' : '',
        isHit ? 'd-tm-qh' : '',
      ].filter(Boolean).join(' ');

      let innerSym = '';
      if (t.status === 'done') {
        innerSym = `<text class="d-tm-nd-sym" x="${t.x}" y="${t.y}" fill="var(--wb-muted-fg)">✓</text>`;
      } else if (t.status === 'dropped') {
        innerSym = `<text class="d-tm-nd-sym" x="${t.x}" y="${t.y}" fill="var(--wb-fail)">×</text>`;
      } else if (t.blocked) {
        innerSym = `<text class="d-tm-nd-sym" x="${t.x}" y="${t.y}" fill="var(--d-tm-on-hc)">!</text>`;
      } else if (t.category === 'epic') {
        innerSym = `<text class="d-tm-nd-sym" x="${t.x}" y="${t.y}" fill="var(--d-tm-on-hc)">E</text>`;
      }

      const shortTitle = t.title.length > 22 ? `${t.title.slice(0, 21)}…` : t.title;
      const dimLbl = (hasSel && !isSel && !isNb) || (hasQ && !isHit);

      return `
        <g class="${cls}" data-tm-node="${t.stem}" style="--d-tm-h:${cl.hue}" tabindex="0" role="button" aria-label="${escapeHtml(t.title)}">
          ${isSel ? `<circle class="d-tm-csel" cx="${t.x}" cy="${t.y}" r="${radius + 5}" />` : ''}
          <circle class="d-tm-ccore" cx="${t.x}" cy="${t.y}" r="${radius}" />
          ${innerSym}
          <text class="d-tm-nd-label${dimLbl ? ' is-dim' : ''}" x="${t.x}" y="${t.y + radius + 13}" text-anchor="middle">${escapeHtml(shortTitle)}</text>
        </g>`;
    }).join('');

    const zoomVal = state.zoom[mode];
    const panVal = state.pan[mode];
    const vpCls = `d-tm-cvp${hasSel ? ' d-tm-hassel' : ''}${hasQ ? ' d-tm-hasq' : ''}`;

    el.innerHTML = `
      <div class="${vpCls}" data-cvp-mode="${mode}">
        <div class="d-tm-ctool" role="group" aria-label="엣지 표시 필터">
          <button type="button" class="d-tm-cet" data-edge-toggle="parent" aria-pressed="${state.edges.parent}">
            <span class="tm-e-sw"></span><span>parent</span>
          </button>
          <button type="button" class="d-tm-cet" data-edge-toggle="blocked" aria-pressed="${state.edges.blocked}">
            <span class="tm-e-sw blk"></span><span>blocked</span>
          </button>
          <button type="button" class="d-tm-cet" data-edge-toggle="related" aria-pressed="${state.edges.related}">
            <span class="tm-e-sw rel"></span><span>related</span>
          </button>
          <button type="button" class="d-tm-cet" data-edge-toggle="sim" aria-pressed="${state.edges.sim}">
            <span class="tm-e-sw sim"></span><span>유사</span>
          </button>
        </div>

        <div class="d-tm-zoom" role="group" aria-label="그래프 줌 컨트롤">
          <button type="button" class="d-tm-zb" data-zoom-act="out" data-zoom-mode="${mode}" title="축소">−</button>
          <span class="d-tm-zrd">${Math.round(zoomVal * 100)}%</span>
          <button type="button" class="d-tm-zb" data-zoom-act="in" data-zoom-mode="${mode}" title="확대">+</button>
          <button type="button" class="d-tm-zb" data-zoom-act="fit" data-zoom-mode="${mode}" title="화면 맞춤">Fit</button>
        </div>

        <svg class="d-tm-csvg" viewBox="0 0 800 530" preserveAspectRatio="xMidYMid meet">
          <defs>
            <marker id="${markerPrefix}-blk" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="6" markerHeight="6" orient="auto">
              <path d="M0 1 L7 4 L0 7 Z" fill="var(--wb-warn)"/>
            </marker>
            <marker id="${markerPrefix}-par" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="5.5" markerHeight="5.5" orient="auto">
              <path d="M0 1 L7 4 L0 7 Z" fill="color-mix(in srgb, var(--wb-fg) 48%, transparent)"/>
            </marker>
          </defs>
          <g class="tm-cam" transform="translate(${400 + panVal.x} ${265 + panVal.y}) scale(${zoomVal}) translate(-400 -265)">
            ${hullsSvg}
            ${edgesSvg}
            ${nodesSvg}
          </g>
        </svg>
      </div>
    `;
  }

  /* ==========================================================================
     Render View B: Hierarchy / Matrix (Docked Compact List vs Fullscreen Matrix)
     ========================================================================== */
  function renderTicketCardHtml(t) {
    const cl = CLUSTER_MAP[t.cluster];
    const isSel = t.stem === state.selectedStem;
    const isSym = t.status === 'done' || t.status === 'dropped';
    const symTxt = t.status === 'done' ? '✓' : t.status === 'dropped' ? '×' : '';
    const pct = t.phasesTotal > 0 ? Math.round((t.phasesDone / t.phasesTotal) * 100) : 0;

    return `
      <div class="d-tm-mwrap">
        <button type="button" class="d-tm-card d-tm-st-${t.status}${isSel ? ' is-sel' : ''}" data-tm-select="${t.stem}" style="--d-tm-h:${cl.hue}">
          <div class="d-tm-ctop">
            <span class="d-tm-sd d-tm-st-${t.status}${isSym ? ' is-sym' : ''}">${symTxt}</span>
            <span class="d-tm-cat d-tm-cat-${t.category}">${t.category}</span>
            <span class="d-tm-ind">
              ${t.blocked ? '<span class="d-tm-bk" title="Blocked">⛔</span>' : ''}
              ${t.odq ? '<span class="d-tm-odq" title="Open Decision Queue">ODQ</span>' : ''}
              ${t.phasesTotal > 0 ? `<span class="d-tm-ph"><i><b style="width:${pct}%"></b></i>${t.phasesDone}/${t.phasesTotal}</span>` : ''}
            </span>
          </div>
          <div class="d-tm-ctt">${escapeHtml(t.title)}</div>
          <div class="d-tm-csub">${escapeHtml(t.stem)}</div>
        </button>
        <button type="button" class="d-tm-mention" data-tm-mention="${t.stem}" title="@${t.stem} 대화창에 멘션 삽입" aria-label="티켓 멘션">@</button>
      </div>`;
  }

  function renderViewB(containerId, isFullscreen) {
    const el = document.getElementById(containerId);
    if (!el) return;

    const q = state.query.trim();
    const visible = getVisibleTickets().filter((t) => matchesQuery(t, q));

    if (!isFullscreen) {
      // Docked Responsive Cluster-Grouped Hierarchy
      el.innerHTML = `
        <div class="tm-vb-docked">
          ${CLUSTERS.filter((cl) => state.clusters.has(cl.id)).map((cl) => {
            const items = visible.filter((t) => t.cluster === cl.id);
            if (!items.length) return '';
            return `
              <section class="tm-vb-cluster" style="--d-tm-h:${cl.hue}">
                <div class="tm-vb-chd">
                  <span class="d-tm-cdot" style="--d-tm-h:${cl.hue}"></span>
                  <span class="tm-vb-cname">${escapeHtml(cl.name)}</span>
                  <span class="tm-vb-ccnt">${items.length}개</span>
                </div>
                <div class="tm-vb-cards">
                  ${items.map(renderTicketCardHtml).join('')}
                </div>
              </section>`;
          }).join('') || `<div style="padding:28px;text-align:center;color:var(--wb-muted-fg);font-size:12px;">조건에 맞는 티켓이 없습니다.</div>`}
        </div>`;
      return;
    }

    // Fullscreen Multi-Column Matrix (Cluster | Ready | Todo | Idea | Done/Dropped)
    const countBy = (stArr) => visible.filter((t) => stArr.includes(t.status)).length;
    el.innerHTML = `
      <div class="d-tm-mx">
        <div class="d-tm-mhead">
          <div class="d-tm-mcol">클러스터</div>
          <div class="d-tm-mcol d-tm-mcol-ready">READY <b>${countBy(['ready'])}</b></div>
          <div class="d-tm-mcol d-tm-mcol-todo">TODO <b>${countBy(['todo'])}</b></div>
          <div class="d-tm-mcol d-tm-mcol-idea">IDEA <b>${countBy(['idea'])}</b></div>
          <div class="d-tm-mcol">DONE / DROPPED <b>${countBy(['done', 'dropped'])}</b></div>
        </div>
        ${CLUSTERS.filter((cl) => state.clusters.has(cl.id)).map((cl) => {
          const items = visible.filter((t) => t.cluster === cl.id);
          const col = (stArr) => {
            const sub = items.filter((t) => stArr.includes(t.status));
            return sub.length ? sub.map(renderTicketCardHtml).join('') : `<div class="d-tm-cempty">—</div>`;
          };
          return `
            <div class="d-tm-lane" style="--d-tm-h:${cl.hue}">
              <div class="tm-vb-chd" style="align-self:start;">
                <span class="d-tm-cdot" style="--d-tm-h:${cl.hue}"></span>
                <span class="tm-vb-cname">${escapeHtml(cl.name)}</span>
                <span class="tm-vb-ccnt">${items.length}</span>
              </div>
              <div class="d-tm-cell">${col(['ready'])}</div>
              <div class="d-tm-cell">${col(['todo'])}</div>
              <div class="d-tm-cell">${col(['idea'])}</div>
              <div class="d-tm-cell">${col(['done', 'dropped'])}</div>
            </div>`;
        }).join('')}
      </div>`;
  }

  /* ==========================================================================
     Render Authentic .d-tm-insp Inspector (Bottom Sheet + Fullscreen 340px Sidebar)
     ========================================================================== */
  function buildInspectorHtml(t) {
    if (!t) {
      return `<div style="padding:12px 4px;color:var(--wb-muted-fg);font-size:12px;">티켓 노드나 카드를 클릭하면 상세 메타데이터와 연결 관계가 표시됩니다.</div>`;
    }
    const cl = CLUSTER_MAP[t.cluster];
    const pct = t.phasesTotal > 0 ? Math.round((t.phasesDone / t.phasesTotal) * 100) : 0;
    const isSym = t.status === 'done' || t.status === 'dropped';
    const symTxt = t.status === 'done' ? '✓' : t.status === 'dropped' ? '×' : '';

    const flagsHtml = [
      t.blocked ? `<span class="d-tm-flag blk">⛔ Blocked</span>` : '',
      t.odq ? `<span class="d-tm-flag odq">ODQ</span>` : '',
    ].filter(Boolean).join('') || `<span class="mono-dim">없음</span>`;

    const linksHtml = t.links.length
      ? t.links.map((l) => {
          const target = TICKET_MAP[l.target];
          if (!target) return '';
          return `
            <button type="button" class="d-tm-il" data-tm-select="${target.stem}">
              <span class="d-tm-rp d-tm-rp-${l.type}">${l.type}</span>
              <span class="d-tm-sd d-tm-st-${target.status}"></span>
              <span class="d-tm-ilt">${escapeHtml(target.title)}</span>
            </button>`;
        }).join('')
      : `<div class="mono-dim">명시적 링크 없음</div>`;

    const simHtml = t.similar.length
      ? t.similar.map((s) => {
          const target = TICKET_MAP[s.target];
          if (!target) return '';
          return `
            <button type="button" class="d-tm-il" data-tm-select="${target.stem}">
              <span class="d-tm-ipc">${s.pct}%</span>
              <span class="d-tm-sd d-tm-st-${target.status}"></span>
              <span class="d-tm-ilt">${escapeHtml(target.title)}</span>
            </button>`;
        }).join('')
      : `<div class="mono-dim">유사 티켓 없음</div>`;

    return `
      <div class="d-tm-ihd">
        <h4 class="d-tm-ititle">${escapeHtml(t.title)}</h4>
        <div class="d-tm-stem">${escapeHtml(t.stem)}</div>
        <button type="button" class="d-tm-mention d-tm-mention-inspector" data-tm-mention="${t.stem}" title="@${t.stem} 대화창에 멘션 삽입" aria-label="멘션 삽입">@</button>
      </div>

      <div class="tm-insp-actions">
        <button type="button" class="d-tm-btn is-pri" data-tm-fullview="${t.stem}">Full View</button>
        <button type="button" class="d-tm-btn" data-tm-openfile="${t.stem}">Open File</button>
      </div>

      <dl class="d-tm-kv">
        <dt>상태</dt>
        <dd><span class="d-tm-badge d-tm-st-${t.status}"><span class="d-tm-sd d-tm-st-${t.status}${isSym ? ' is-sym' : ''}">${symTxt}</span>${t.status}</span></dd>

        <dt>클러스터</dt>
        <dd><span class="d-tm-cpill" style="--d-tm-h:${cl.hue}"><span class="d-tm-cdot" style="--d-tm-h:${cl.hue}"></span>${escapeHtml(cl.name)}</span></dd>

        <dt>카테고리</dt>
        <dd><span class="d-tm-cat d-tm-cat-${t.category}">${t.category}</span></dd>

        <dt>페이즈</dt>
        <dd>
          ${t.phasesTotal > 0
            ? `<span class="d-tm-phbar"><i><b style="width:${pct}%"></b></i>${t.phasesDone}/${t.phasesTotal} (${pct}%)</span>`
            : `<span class="mono-dim">리서치 (페이즈 없음)</span>`}
        </dd>

        <dt>플래그</dt>
        <dd>${flagsHtml}</dd>

        <dt>담당</dt>
        <dd><span class="mono-dim">${escapeHtml(t.assignee)}</span></dd>

        <dt>분량</dt>
        <dd><span class="mono-dim">${t.lines}줄</span></dd>
      </dl>

      <div class="d-tm-isec">명시적 링크 <b>${t.links.length}</b></div>
      <div class="d-tm-ilist">${linksHtml}</div>

      <div class="d-tm-isec">유사 티켓 <b>${t.similar.length}</b></div>
      <div class="d-tm-ilist">${simHtml}</div>
    `;
  }

  function syncInspector() {
    const t = TICKET_MAP[state.selectedStem] || null;
    const html = buildInspectorHtml(t);
    tmDockInspBody.innerHTML = html;
    tmFullInspBody.innerHTML = html;
    tmSheetStem.textContent = t ? t.stem : '선택된 티켓 없음';

    tmDockSheet.hidden = !state.sheetOpen;
    tmDockSheet.classList.toggle('is-collapsed', state.sheetCollapsed);
    mSheetToggle.setAttribute('aria-pressed', String(state.sheetOpen && !state.sheetCollapsed));
    mSheetLbl.textContent = state.sheetOpen && !state.sheetCollapsed ? '인스펙터 ON' : '인스펙터 OFF';
  }

  function syncViewButtons() {
    document.querySelectorAll('.d-tm-segb[data-tm-view]').forEach((btn) => {
      btn.setAttribute('aria-pressed', String(btn.dataset.tmView === state.tmView));
    });
    document.getElementById('tm-dock-view-c').hidden = state.tmView !== 'c';
    document.getElementById('tm-dock-view-b').hidden = state.tmView !== 'b';
    document.getElementById('tm-full-view-c').hidden = state.tmView !== 'c';
    document.getElementById('tm-full-view-b').hidden = state.tmView !== 'b';
  }

  function syncSearchUi() {
    const q = state.query.trim();
    const visible = getVisibleTickets();
    const hits = q ? visible.filter((t) => matchesQuery(t, q)).length : visible.length;
    const hcHtml = q ? `${hits}/${visible.length}` : `<kbd class="d-tm-kbd">/</kbd>`;
    document.getElementById('tm-dock-hc').innerHTML = hcHtml;
    document.getElementById('tm-full-hc').innerHTML = hcHtml;
    tmFullStats.textContent = `${visible.length}개 티켓 · ${state.clusters.size}개 클러스터`;
  }

  function renderAllTicketMap() {
    renderStatusPills('tm-dock-pills', false);
    renderStatusPills('tm-full-pills', true);
    renderClusterPopover('tm-dock-cl-pop', 'tm-dock-cl-cnt');
    renderClusterPopover('tm-full-cl-pop', 'tm-full-cl-cnt');
    syncViewButtons();
    syncSearchUi();
    renderViewC('tm-dock-view-c', 'dock');
    renderViewB('tm-dock-view-b', false);
    if (!tmBack.hidden) {
      renderViewC('tm-full-view-c', 'full');
      renderViewB('tm-full-view-b', true);
    }
    syncInspector();
  }

  function selectTicket(stem, openSheet = true) {
    if (!TICKET_MAP[stem]) return;
    state.selectedStem = stem;
    if (openSheet) {
      state.sheetOpen = true;
      state.sheetCollapsed = false;
    }
    renderAllTicketMap();
  }

  function openFullViewModal(stem) {
    const t = TICKET_MAP[stem];
    if (!t) return;
    const cl = CLUSTER_MAP[t.cluster];
    document.getElementById('tm-fmodal-title').textContent = t.title;
    document.getElementById('tm-fmodal-body').innerHTML = `
      <div style="display:flex;gap:6px;flex-wrap:wrap;align-items:center;margin-bottom:10px;">
        <span class="d-tm-badge d-tm-st-${t.status}">${t.status}</span>
        <span class="d-tm-cpill" style="--d-tm-h:${cl.hue}">${escapeHtml(cl.name)}</span>
        <span class="d-tm-cat d-tm-cat-${t.category}">${t.category}</span>
        <span class="mono-dim">${escapeHtml(t.stem)}.md · ${t.lines}줄</span>
      </div>
      <h4>요약</h4>
      <p>${escapeHtml(t.summary)}</p>
      <h4>핵심 계약 및 경로</h4>
      <ul>
        <li>파일 경로: <code>ai-docs/tickets/${t.status}/${escapeHtml(t.stem)}.md</code></li>
        <li>담당자: <code>${escapeHtml(t.assignee)}</code></li>
        <li>명시적 링크: ${t.links.length}건 · 유사 티켓: ${t.similar.length}건</li>
      </ul>
    `;
    tmFullModal.hidden = false;
  }

  /* ==========================================================================
     Event Listeners
     ========================================================================== */
  // Preset Switcher
  document.querySelectorAll('.m-tab[data-preset]').forEach((btn) => {
    btn.addEventListener('click', () => applyPreset(btn.dataset.preset));
  });

  // Dark / Light Theme Toggle
  document.getElementById('m-theme-toggle').addEventListener('click', (e) => {
    state.theme = state.theme === 'dark' ? 'light' : 'dark';
    root.dataset.theme = state.theme;
    e.currentTarget.setAttribute('aria-pressed', String(state.theme === 'light'));
    document.getElementById('m-theme-lbl').textContent = state.theme === 'dark' ? 'Dark' : 'Light';
  });

  // Inspector Bottom-Sheet Demo Toggle
  mSheetToggle.addEventListener('click', () => {
    if (state.sheetOpen && !state.sheetCollapsed) {
      state.sheetOpen = false;
    } else {
      state.sheetOpen = true;
      state.sheetCollapsed = false;
    }
    syncInspector();
  });

  // Left Rail Collapse Toggle
  document.getElementById('railbtn').addEventListener('click', (e) => {
    const collapsed = root.dataset.railCollapsed === 'true';
    root.dataset.railCollapsed = String(!collapsed);
    e.currentTarget.setAttribute('aria-expanded', String(collapsed));
  });

  // Top-right legacy handles feedback
  document.getElementById('ghandle').addEventListener('click', () => {
    showToast('임시 레거시 #ghandle 드로어 (Track 2B에서 #bgal Git 페이지로 이관 예정)');
  });
  document.getElementById('drhandle').addEventListener('click', () => {
    showToast('임시 레거시 #drhandle 드로어 (gallery-workpanel-design 이관 전까지 유지)');
  });
  document.getElementById('tk-open').addEventListener('click', () => {
    showToast('전역 티켓 트래커 (#tkhandles) — #thandle은 은퇴하고 #bgal로 통합됨');
  });

  // .gal-rail Click & Hover Popover
  const PAGE_ORDER_WIDE = ['p1', 'plus', 'workpanel', 'ticketmap', 'git'];
  const PAGE_ORDER_NARROW = ['main', 'p1', 'plus', 'workpanel', 'ticketmap', 'git'];

  galRail.addEventListener('click', (e) => {
    const dot = e.target.closest('.gal-dot[data-page]');
    if (dot) {
      setActivePage(dot.dataset.page);
      return;
    }
    if (e.target.closest('#gal-prev') || e.target.closest('#gal-next')) {
      const order = root.dataset.gal === 'narrow' ? PAGE_ORDER_NARROW : PAGE_ORDER_WIDE;
      const idx = Math.max(0, order.indexOf(state.activePage));
      const delta = e.target.closest('#gal-prev') ? -1 : 1;
      const next = order[(idx + delta + order.length) % order.length];
      setActivePage(next);
    }
  });

  galRail.addEventListener('pointerover', (e) => {
    const dot = e.target.closest('.gal-dot[data-page]');
    if (dot) showGalPop(dot);
  });
  galRail.addEventListener('pointerout', (e) => {
    if (!e.relatedTarget || !galRail.contains(e.relatedTarget) || !e.relatedTarget.closest('.gal-dot[data-page]')) {
      hideGalPop();
    }
  });

  // Jump from Git / Workpanel placeholder directly to ticket in Ticket Map
  document.addEventListener('click', (e) => {
    const jump = e.target.closest('[data-jump-ticket]');
    if (jump) {
      setActivePage('ticketmap');
      selectTicket(jump.dataset.jumpTicket, true);
      showToast(`티켓 맵에서 ${jump.dataset.jumpTicket} 선택됨`);
      return;
    }
    const unpin = e.target.closest('[data-unpin-demo]');
    if (unpin) {
      showToast('사용자 핀 카드는 언제든 화이트보드로 고정 해제할 수 있습니다 (고정 시스템 페이지는 해제 불가)');
    }
  });

  // #bgal Left Resize Handle (.bp-edge) — Live @container testing!
  bgalEdge.addEventListener('pointerdown', (e) => {
    if (state.preset === 'three-panel') return;
    e.preventDefault();
    const startX = e.clientX;
    const startW = bgal.getBoundingClientRect().width;
    bgalEdge.classList.add('drag');
    document.body.classList.add('is-resizing');

    const onMove = (ev) => {
      const nextW = Math.max(350, Math.min(720, Math.round(startW - (ev.clientX - startX))));
      state.bgalWidth = nextW;
      if (state.preset === 'narrow-col') {
        root.dataset.preset = 'wide-tm';
        state.preset = 'wide-tm';
      }
      root.style.setProperty('--bgal-w', `${nextW}px`);
      mBgalWidth.textContent = `#bgal · ${nextW}px${nextW <= 410 ? ' (CQ)' : ''}`;
    };
    const onUp = () => {
      bgalEdge.classList.remove('drag');
      document.body.classList.remove('is-resizing');
      window.removeEventListener('pointermove', onMove);
      window.removeEventListener('pointerup', onUp);
    };
    window.addEventListener('pointermove', onMove);
    window.addEventListener('pointerup', onUp);
  });

  // Ticket Map Global Delegated Clicks (View tabs, Status pills, Edges, Nodes, Inspector links, Fullscreen)
  document.addEventListener('click', (e) => {
    const viewBtn = e.target.closest('.d-tm-segb[data-tm-view]');
    if (viewBtn) {
      state.tmView = viewBtn.dataset.tmView;
      renderAllTicketMap();
      return;
    }

    const stBtn = e.target.closest('[data-status-toggle]');
    if (stBtn) {
      const st = stBtn.dataset.statusToggle;
      if (state.statuses.has(st)) {
        if (state.statuses.size > 1) state.statuses.delete(st);
      } else {
        state.statuses.add(st);
      }
      renderAllTicketMap();
      return;
    }

    const edgeBtn = e.target.closest('[data-edge-toggle]');
    if (edgeBtn) {
      const kind = edgeBtn.dataset.edgeToggle;
      state.edges[kind] = !state.edges[kind];
      renderAllTicketMap();
      return;
    }

    const zoomBtn = e.target.closest('[data-zoom-act]');
    if (zoomBtn) {
      const mode = zoomBtn.dataset.zoomMode;
      const act = zoomBtn.dataset.zoomAct;
      if (act === 'in') state.zoom[mode] = Math.min(1.8, +(state.zoom[mode] + 0.15).toFixed(2));
      if (act === 'out') state.zoom[mode] = Math.max(0.65, +(state.zoom[mode] - 0.15).toFixed(2));
      if (act === 'fit') {
        state.zoom[mode] = 1;
        state.pan[mode] = { x: 0, y: 0 };
      }
      renderAllTicketMap();
      return;
    }

    const mentionBtn = e.target.closest('[data-tm-mention]');
    if (mentionBtn) {
      e.stopPropagation();
      const stem = mentionBtn.dataset.tmMention;
      const composer = document.getElementById('composer-input');
      if (composer) {
        composer.innerHTML = `<code>@${escapeHtml(stem)}</code> 확인 부탁드립니다.<span class="caret"></span>`;
      }
      showToast(`@${stem} 대화창 입력란에 삽입됨`);
      return;
    }

    const fullViewBtn = e.target.closest('[data-tm-fullview]');
    if (fullViewBtn) {
      openFullViewModal(fullViewBtn.dataset.tmFullview);
      return;
    }

    const openFileBtn = e.target.closest('[data-tm-openfile]');
    if (openFileBtn) {
      const t = TICKET_MAP[openFileBtn.dataset.tmOpenfile];
      if (t) showToast(`파일 열기: ai-docs/tickets/${t.status}/${t.stem}.md`);
      return;
    }

    const node = e.target.closest('[data-tm-node]');
    if (node) {
      selectTicket(node.dataset.tmNode, true);
      return;
    }

    const sel = e.target.closest('[data-tm-select]');
    if (sel) {
      selectTicket(sel.dataset.tmSelect, true);
      return;
    }

    // Close cluster popovers on outside click
    if (!e.target.closest('.d-tm-clwrap')) {
      document.getElementById('tm-dock-cl-pop').hidden = true;
      document.getElementById('tm-full-cl-pop').hidden = true;
      document.getElementById('tm-dock-cl-btn').setAttribute('aria-expanded', 'false');
      document.getElementById('tm-full-cl-btn').setAttribute('aria-expanded', 'false');
    }
  });

  // Cluster Popover Triggers & Checkboxes
  function bindClusterPop(btnId, popId) {
    const btn = document.getElementById(btnId);
    const pop = document.getElementById(popId);
    btn.addEventListener('click', (e) => {
      e.stopPropagation();
      const willOpen = pop.hidden;
      pop.hidden = !willOpen;
      btn.setAttribute('aria-expanded', String(willOpen));
    });
    pop.addEventListener('change', (e) => {
      const input = e.target;
      if (input.hasAttribute('data-cl-all')) {
        if (input.checked) {
          CLUSTERS.forEach((c) => state.clusters.add(c.id));
        } else {
          state.clusters = new Set([CLUSTERS[0].id]);
        }
      } else if (input.dataset.clId) {
        if (input.checked) {
          state.clusters.add(input.dataset.clId);
        } else if (state.clusters.size > 1) {
          state.clusters.delete(input.dataset.clId);
        }
      }
      renderAllTicketMap();
    });
  }
  bindClusterPop('tm-dock-cl-btn', 'tm-dock-cl-pop');
  bindClusterPop('tm-full-cl-btn', 'tm-full-cl-pop');

  // Synced Search Inputs
  const dockQ = document.getElementById('tm-dock-q');
  const fullQ = document.getElementById('tm-full-q');
  function onSearchInput(val) {
    state.query = val;
    if (dockQ.value !== val) dockQ.value = val;
    if (fullQ.value !== val) fullQ.value = val;
    renderAllTicketMap();
  }
  dockQ.addEventListener('input', (e) => onSearchInput(e.target.value));
  fullQ.addEventListener('input', (e) => onSearchInput(e.target.value));

  // Refresh Buttons
  ['tm-dock-refresh', 'tm-full-refresh'].forEach((id) => {
    document.getElementById(id).addEventListener('click', () => {
      showToast('티켓 맵 인덱스 동기화 완료 (12개 티켓)');
    });
  });

  // Fullscreen Expand (⤢) & Close (#tmback)
  document.getElementById('tm-dock-expand').addEventListener('click', () => applyPreset('fullscreen'));
  document.getElementById('tm-sheet-open-full').addEventListener('click', () => applyPreset('fullscreen'));
  document.getElementById('tm-full-close').addEventListener('click', () => applyPreset(state.prevPreset || 'wide-tm'));

  // Bottom-Sheet Collapse / Close
  document.getElementById('tm-sheet-collapse').addEventListener('click', () => {
    state.sheetCollapsed = !state.sheetCollapsed;
    syncInspector();
  });
  document.getElementById('tm-sheet-close').addEventListener('click', () => {
    state.sheetOpen = false;
    syncInspector();
  });

  // Fullscreen Right Inspector Minimize Toggle
  document.getElementById('tm-full-imin').addEventListener('click', () => {
    state.fullInspMin = !state.fullInspMin;
    tmFullInsp.classList.toggle('d-tm-insp-min', state.fullInspMin);
  });

  // Full View Modal Close
  document.getElementById('tm-fmodal-close').addEventListener('click', () => {
    tmFullModal.hidden = true;
  });
  tmFullModal.addEventListener('click', (e) => {
    if (e.target === tmFullModal) tmFullModal.hidden = true;
  });

  // Graph Node Hover Tooltip & Panning
  document.addEventListener('pointermove', (e) => {
    const node = e.target.closest('[data-tm-node]');
    if (!node) {
      tmCtip.classList.remove('on');
      return;
    }
    const t = TICKET_MAP[node.dataset.tmNode];
    if (!t) return;
    const cl = CLUSTER_MAP[t.cluster];
    tmCtip.innerHTML = `
      <div class="d-tm-ttt">${escapeHtml(t.title)}</div>
      <div class="d-tm-tts">${escapeHtml(t.stem)}</div>
      <div class="d-tm-ttr">
        <span class="d-tm-badge d-tm-st-${t.status}">${t.status}</span>
        <span class="d-tm-cpill" style="--d-tm-h:${cl.hue}">${escapeHtml(cl.name)}</span>
        <span class="d-tm-cat d-tm-cat-${t.category}">${t.category}</span>
      </div>
    `;
    tmCtip.classList.add('on');
    const pad = 14;
    const x = Math.min(window.innerWidth - 292, e.clientX + pad);
    const y = Math.min(window.innerHeight - 108, e.clientY + pad);
    tmCtip.style.transform = `translate3d(${Math.round(x)}px, ${Math.round(y)}px, 0)`;
  });

  // Drag Pan inside SVG Graph
  document.addEventListener('pointerdown', (e) => {
    const cvp = e.target.closest('.d-tm-cvp');
    if (!cvp || e.target.closest('[data-tm-node], .d-tm-ctool, .d-tm-zoom')) return;
    const mode = cvp.dataset.cvpMode;
    const startX = e.clientX;
    const startY = e.clientY;
    const orig = { ...state.pan[mode] };
    cvp.classList.add('d-tm-grab');

    const onMove = (ev) => {
      state.pan[mode] = {
        x: Math.max(-220, Math.min(220, orig.x + (ev.clientX - startX))),
        y: Math.max(-160, Math.min(160, orig.y + (ev.clientY - startY))),
      };
      const cam = cvp.querySelector('.tm-cam');
      if (cam) {
        const z = state.zoom[mode];
        cam.setAttribute('transform', `translate(${400 + state.pan[mode].x} ${265 + state.pan[mode].y}) scale(${z}) translate(-400 -265)`);
      }
    };
    const onUp = () => {
      cvp.classList.remove('d-tm-grab');
      window.removeEventListener('pointermove', onMove);
      window.removeEventListener('pointerup', onUp);
    };
    window.addEventListener('pointermove', onMove);
    window.addEventListener('pointerup', onUp);
  });

  // Container-Scoped Keyboard Focus Tracking for Docked Ticket Map
  tmDocked.addEventListener('focusin', () => {
    state.tmFocused = true;
    tmDocked.classList.add('is-focused');
  });
  tmDocked.addEventListener('focusout', (e) => {
    if (!tmDocked.contains(e.relatedTarget)) {
      state.tmFocused = false;
      tmDocked.classList.remove('is-focused');
    }
  });

  // Keyboard Shortcuts: Window-wide ONLY when #tmback is open; Container-scoped when docked!
  document.addEventListener('keydown', (e) => {
    if (!tmFullModal.hidden && e.key === 'Escape') {
      tmFullModal.hidden = true;
      return;
    }

    const inInput = e.target && (e.target.tagName === 'INPUT' || e.target.tagName === 'TEXTAREA');

    // Fullscreen overlay owns window shortcuts cleanly
    if (!tmBack.hidden) {
      if (e.key === 'Escape') {
        applyPreset(state.prevPreset || 'wide-tm');
        return;
      }
      if (!inInput) {
        if (e.key === '1') { state.tmView = 'b'; renderAllTicketMap(); }
        if (e.key === '2') { state.tmView = 'c'; renderAllTicketMap(); }
        if (e.key === '/') { e.preventDefault(); fullQ.focus(); }
      }
      return;
    }

    // Docked mode: container-scoped to #tm-docked focus only!
    if (state.tmFocused && state.activePage === 'ticketmap') {
      if (e.key === 'Escape') {
        if (inInput) {
          dockQ.blur();
          tmDocked.focus();
        } else if (state.sheetOpen) {
          state.sheetOpen = false;
          syncInspector();
        }
        return;
      }
      if (!inInput) {
        if (e.key === '1') { state.tmView = 'b'; renderAllTicketMap(); }
        if (e.key === '2') { state.tmView = 'c'; renderAllTicketMap(); }
        if (e.key === '/') { e.preventDefault(); dockQ.focus(); }
      }
    }
  });

  // Initial Render
  applyPreset('wide-tm');
})();
