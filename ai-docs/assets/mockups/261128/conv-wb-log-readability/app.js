(() => {
  'use strict';

  const ICONS = {
    robot: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="4" y="8" width="16" height="11" rx="3"/><path d="M12 4v4"/><circle cx="9.5" cy="13.5" r="1"/><circle cx="14.5" cy="13.5" r="1"/></svg>',
    board: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="4" width="18" height="14" rx="2"/><path d="M8 21h8M12 18v3"/></svg>',
    check: '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="M3.2 8.4 6.5 11.7 13 4.8"/></svg>'
  };

  const VARIANTS = {
    baseline: {
      id: 'baseline',
      badge: '0 · 현재 (Baseline)',
      title: '현재 프로덕션 렌더링',
      chips: ['12px / 1.5', '--wb-muted-fg', '백틱 원문 노출', 'align-items: baseline'],
      note: '현재 상태 — 12px 회색 원문(`develop`, `7fbf802bd` 등)이 굵은 토픽 바와 주황 정책 캡슐 사이에서 시선이 쉽게 건너뜁니다.',
      pro: '가장 낮은 시각 무게',
      con: '원문 백틱 노이즈 + 저대비로 요약 로그 누락 빈번'
    },
    a: {
      id: 'a',
      badge: '시안 A',
      title: '인라인 마크다운 + 톤 업 (Quiet Ink Lift)',
      chips: ['12.5px / 1.62', '~80% fg 톤업', '인라인 MD 파싱', '무장식·L 상단정렬'],
      note: '시안 A (Quiet Ink Lift) — 장식·박스 추가 없이 인라인 마크다운(`code` 칩)과 본문 잉크 80% 톤업, L 뱃지 첫 줄 정렬만으로 가독성을 끌어올립니다.',
      pro: '가장 조용하고 카드 밀도 유지, 회귀 위험 최소',
      con: '긴 로그가 패널/캡슐과 섞일 때 행 경계 인지는 다소 부드러움'
    },
    b: {
      id: 'b',
      badge: '시안 B',
      title: '미세 좌측 레일 + 인라인 마크다운 (Whisper Left Rail)',
      chips: ['12.5px / 1.62', '~84% fg', '2px 32% 프라이머리 레일', '소프트 스퀘어클 L'],
      note: '시안 B (Whisper Left Rail) — 패널(3px 100% + 13% 틴트)보다 확실히 약한 2px 32% 좌측 레일로 눈의 진입점을 잡아주면서 로그 위계를 지킵니다.',
      pro: '긴 멀티라인 로그에서도 시선 고정이 매우 자연스러움',
      con: '패널 바로 아래에 붙을 때 좌측 수직선 계층(3px vs 2px) 구분감 확인 필요'
    },
    c: {
      id: 'c',
      badge: '시안 C',
      title: '소프트 리세스 로그 웰 (Soft Tonal Log Well)',
      chips: ['12.5px / 1.62', '~85% fg', '보더리스 리세스 웰', '연속 로그 묶음 + 미세 구분선'],
      note: '시안 C (Soft Tonal Log Well) — 카드 내부에 한 단계 가라앉은 보더리스 톤 웰을 파서 "읽기 전용 로그 슬롯"을 분리하고, 연속 로그는 하나의 웰 안에서 미세 구분선으로 정돈합니다.',
      pro: '토픽 바·정책 캡슐과 영역 분리가 가장 깔끔함 (Werkschaft 톤 래더 정합)',
      con: '단일 짧은 로그에도 웰 패딩이 더해져 카드 세로 높이가 소폭 증가'
    },
    d: {
      id: 'd',
      badge: '시안 D',
      title: '스캔형 로그 칩 + 절제된 위계 (Structured Scannable Log)',
      chips: ['12.5px / 1.62', '~84% fg', '로그 마이크로 칩 + 세로 트랙', '코드 칩 대비 강화'],
      note: '시안 D (Structured Scannable Log) — 작은 원형 L 대신 읽기 쉬운 `로그` 마이크로 칩과 멀티라인·연속 로그를 잇는 미세 세로 타임라인 트랙으로 활동 이력처럼 스캔됩니다.',
      pro: '연속 2~3건 로그 스캔성과 `L` 기호의 의미 전달력이 가장 높음',
      con: '좌측 거터(30px) 사용으로 좁은 폭에서 텍스트 가로폭이 소폭 줄어듦'
    }
  };

  // Sample data: Exact screenshot scenario ("real") & mixed/multi-log scenario ("mixed")
  const SCENARIOS = {
    real: {
      time: '13:42',
      topics: [
        {
          label: '로컬 데몬 스왑 변경 이력',
          name: 'daemon-swap-changelog',
          target: 'topic-daemon-swap',
          rows: [
            {
              kind: 'log',
              id: 'e-1841',
              raw: '`develop` 최신 커밋 `7fbf802bd` 반영 완료 — `wsconvidx` 대화 인덱스 패키지와 MCP 도구 `conv.list`/`conv.search`/`conv.read`/`conv.reveal`, 세션 권한 토글(`conv_history_access`)까지 모두 통합했습니다. 관련 티켓 `261128-feat-conv-history-mcp-api-and-session-permission-toggle`은 `.done/`으로 닫고 로컬 데몬 바이너리 스왑 후 헬스체크 통과 확인했습니다.',
              html: '<code>develop</code> 최신 커밋 <code>7fbf802bd</code> 반영 완료 — <code>wsconvidx</code> 대화 인덱스 패키지와 MCP 도구 <code>conv.list</code>/<code>conv.search</code>/<code>conv.read</code>/<code>conv.reveal</code>, 세션 권한 토글(<code>conv_history_access</code>)까지 모두 통합했습니다. 관련 티켓 <code>261128-feat-conv-history-mcp-api-and-session-permission-toggle</code>은 <code>.done/</code>으로 닫고 <strong>로컬 데몬 바이너리 스왑 후 헬스체크 통과</strong> 확인했습니다.'
            }
          ],
          items: []
        },
        {
          label: '이전 대화 기록 검색 MCP API & 세션 권한 토글',
          name: 'conv-history-mcp-api',
          target: 'topic-conv-history',
          rows: [
            {
              kind: 'log',
              id: 'e-1842',
              raw: '구현 및 통합 테스트(`go test ./internal/wsconvidx/... ./internal/wsdashboard/...`) 전건 통과했습니다. 다만 병합 직전 `ws/git.merge` 사이즈 가드에서 기존 대형 파일 3건에 대한 분리 권고가 보고되어 {{1654}} 정책 확인을 올려두었습니다.',
              html: '구현 및 통합 테스트(<code>go test ./internal/wsconvidx/... ./internal/wsdashboard/...</code>) 전건 통과했습니다. 다만 병합 직전 <code>ws/git.merge</code> 사이즈 가드에서 기존 대형 파일 3건에 대한 분리 권고가 보고되어 <span class="wbref" tabindex="0" role="link" data-ref="1654">{{1654}}</span> 정책 확인을 올려두었습니다.'
            }
          ],
          items: [
            {
              id: '1654',
              cls: 'policy',
              clsLabel: '정책',
              state: 'open',
              title: 'ws/git.merge 사이즈 가드 권고 3건(runner_conv.go 2598줄, conv_host.go 1147줄, sse.go 1012줄) 처리 방침'
            }
          ]
        }
      ]
    },
    mixed: {
      time: '13:44',
      topics: [
        {
          label: '이전 대화 기록 검색 MCP API & 세션 권한 토글',
          name: 'conv-history-mcp-api',
          target: 'topic-conv-history-2',
          rows: [
            {
              kind: 'panel',
              id: 'p-19',
              title: '권한 토글 UX · 인덱스 스키마 요약 (v2)',
              preview: '<p>세션 설정 팝오버에 <code>conv_history_access</code> 기본값(<code>off</code>) 스위치를 추가하고, 허용 시에만 <code>conv.search</code>·<code>conv.read</code> 스코프를 노출합니다.</p><p>인덱스 갱신은 append-only 델타를 메모리에 유지하고 백그라운드 커밋 시점에만 플러시합니다.</p>'
            },
            {
              kind: 'log',
              id: 'e-1845',
              raw: '워커 1차 리뷰 완료 — `conv.read` 바이트 상한 검증 누락 1건(Important)을 수정해 `7fbf802bd`에 추가 커밋했습니다.',
              html: '워커 1차 리뷰 완료 — <code>conv.read</code> 바이트 상한 검증 누락 1건(<strong>Important</strong>)을 수정해 <code>7fbf802bd</code>에 추가 커밋했습니다.'
            },
            {
              kind: 'log',
              id: 'e-1846',
              raw: '`conv.reveal`이 닫힌 세션을 열 때 권한 토글이 꺼져 있으면 명시적 거부 사유(`permission_denied`)를 반환하도록 회귀 테스트 4건을 보강했습니다.',
              html: '<code>conv.reveal</code>이 닫힌 세션을 열 때 권한 토글이 꺼져 있으면 명시적 거부 사유(<code>permission_denied</code>)를 반환하도록 회귀 테스트 4건을 보강했습니다.'
            },
            {
              kind: 'log',
              id: 'e-1847',
              raw: '로컬 데몬 재기동 후 실제 대시보드에서 토글 on/off 전환과 {{1654}} 정책 항목 링크 점프까지 확인 마쳤습니다.',
              html: '로컬 데몬 재기동 후 실제 대시보드에서 토글 on/off 전환과 <span class="wbref" tabindex="0" role="link" data-ref="1654">{{1654}}</span> 정책 항목 링크 점프까지 확인 마쳤습니다.'
            }
          ],
          items: [
            {
              id: '1654',
              cls: 'policy',
              clsLabel: '정책',
              state: 'open',
              title: 'ws/git.merge 사이즈 가드 권고 3건(runner_conv.go 2598줄, conv_host.go 1147줄, sse.go 1012줄) 처리 방침'
            },
            {
              id: '1649',
              cls: 'technical',
              clsLabel: '기술',
              state: 'settled',
              title: 'wsconvidx 스냅샷 캐시 경로를 클론 공유 디렉터리로 통일'
            }
          ]
        }
      ]
    }
  };

  const params = new URLSearchParams(window.location.search);
  const pick = (k, allowed, fallback) => {
    const v = params.get(k);
    return allowed.includes(v) ? v : fallback;
  };
  const state = {
    view: pick('view', ['compare', 'context'], 'compare'),
    scenario: pick('scenario', ['real', 'mixed'], 'real'),
    theme: pick('theme', ['dark', 'light'], 'dark'),
    variant: pick('variant', ['baseline', 'a', 'b', 'c', 'd'], 'a'),
    split: params.get('split') === '1'
  };

  function escHtml(s) {
    return String(s)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  function renderLogRow(row, variantId) {
    const body = variantId === 'baseline' ? escHtml(row.raw) : row.html;
    if (variantId === 'd') {
      return `
        <div class="wbupdate-row wbupdate-log" data-wb-element="${row.id}" tabindex="0" role="link">
          <span class="wbupdate-d-gutter" aria-hidden="true">
            <span class="wbupdate-d-tag">로그</span>
            <span class="wbupdate-d-track"></span>
          </span>
          <span class="wbupdate-text">${body}</span>
          <span class="wbupdate-d-jump" aria-hidden="true">↗</span>
        </div>`;
    }
    return `
      <div class="wbupdate-row wbupdate-log" data-wb-element="${row.id}" tabindex="0" role="link">
        <span class="wbupdate-l" aria-hidden="true">L</span>
        <span class="wbupdate-text">${body}</span>
      </div>`;
  }

  function renderPanelRow(row) {
    return `
      <div class="wbupdate-panel-wrap" data-wb-element="${row.id}">
        <div class="wbupdate-row wbupdate-panel wbupdate-panel-has-preview" data-wb-element="${row.id}" tabindex="0" role="link">
          <span class="wbupdate-panel-label">${ICONS.board}패널</span>
          <span class="wbupdate-text">${escHtml(row.title)}</span>
          <a href="#${row.id}" data-wb-jump="${row.id}">#${row.id}</a>
        </div>
        <div class="wbupdate-panel-body md">${row.preview}</div>
      </div>`;
  }

  function renderTopicRows(rows, variantId) {
    // Group consecutive log rows for Variant C (shared recessed well) and Variant D (continuous timeline)
    let out = '';
    let i = 0;
    while (i < rows.length) {
      if (rows[i].kind === 'panel') {
        out += renderPanelRow(rows[i]);
        i += 1;
        continue;
      }
      if (variantId === 'c' || variantId === 'd') {
        const cls = variantId === 'c' ? 'wbupdate-log-well' : 'wbupdate-log-timeline';
        let inner = '';
        while (i < rows.length && rows[i].kind === 'log') {
          inner += renderLogRow(rows[i], variantId);
          i += 1;
        }
        out += `<div class="${cls}">${inner}</div>`;
      } else {
        out += renderLogRow(rows[i], variantId);
        i += 1;
      }
    }
    return out;
  }

  function renderItems(items) {
    if (!items || !items.length) return '';
    const list = items.map(it => {
      const settledAttr = it.state === 'settled' ? ' data-wb-state="settled"' : '';
      return `
        <div class="wbupdate-qitem"${settledAttr}>
          <a class="wbupdate-cap wbupdate-cls-${it.cls}" href="#item-${it.id}" data-wb-jump="item-${it.id}">
            <span class="wbupdate-cap-id">(${it.id})</span>
            <span class="wbupdate-cap-cls">${it.clsLabel}</span>
            <span class="wbupdate-cap-ttl">${escHtml(it.title)}</span>
            <span class="wbupdate-cap-mark" aria-hidden="true">${ICONS.check}</span>
            <span class="wbupdate-cap-arr" aria-hidden="true">↗</span>
          </a>
        </div>`;
    }).join('');
    return `<div class="wbupdate-items wbupdate-hydrated">${list}</div>`;
  }

  function renderWbUpdateCard(scenarioKey, variantId) {
    const scn = SCENARIOS[scenarioKey];
    const topicsHtml = scn.topics.map(t => `
      <section class="wbupdate-topic" data-wb-topic="${t.target}">
        <div class="wbupdate-bar">
          <b>${escHtml(t.label)}</b>
          <span class="wbupdate-key">${escHtml(t.name)}</span>
          <a href="#${t.target}" data-wb-jump="${t.target}">토픽 ↗</a>
        </div>
        <div class="wbupdate-rows">
          ${renderTopicRows(t.rows, variantId)}
          ${renderItems(t.items)}
        </div>
        <span class="wbupdate-status" role="status" aria-live="polite"></span>
      </section>`).join('');

    return `
      <article class="wbeffects wbupdate" data-variant="${variantId}">
        <div class="wbupdate-head">
          <b>화이트보드 업데이트</b>
          <time class="t">${scn.time}</time>
        </div>
        ${topicsHtml}
      </article>`;
  }

  function renderCompareView() {
    const baseStage = document.getElementById('baseline-stage');
    baseStage.innerHTML = renderWbUpdateCard(state.scenario, 'baseline');

    const grid = document.getElementById('variants-grid');
    grid.innerHTML = ['a', 'b', 'c', 'd'].map(vid => {
      const v = VARIANTS[vid];
      return `
        <div class="cmp-slot">
          <div class="cmp-slot-head">
            <span class="cmp-tag is-opt">${v.badge}</span>
            <span class="cmp-slot-title">${v.title}</span>
            <button type="button" class="cmp-jump-btn" data-open-context="${vid}">1:1 보기 →</button>
          </div>
          <div class="cmp-slot-meta">
            ${v.chips.map(c => `<span class="cmp-chip">${escHtml(c)}</span>`).join('')}
          </div>
          ${renderWbUpdateCard(state.scenario, vid)}
          <div class="cmp-tradeoff">
            <span><b>장점</b> ${escHtml(v.pro)}</span>
            <span><b>고려</b> ${escHtml(v.con)}</span>
          </div>
        </div>`;
    }).join('');
  }

  function renderChatColumn(variantId, headerLabel) {
    const v = VARIANTS[variantId];
    return `
      <section class="chat-frame" aria-label="${escHtml(headerLabel)}">
        <div class="chat-frame-head">
          <span class="cf-left"><span class="cf-dot"></span>${escHtml(headerLabel)}</span>
          <span class="cf-meta">640px · ${escHtml(v.chips[0])}</span>
        </div>
        <div class="conv">
          <div class="msg user">
            <div class="mh"><time class="t">13:40</time></div>
            <div class="bub">이전 대화 검색 MCP API 통합 끝났으면 로컬 데몬까지 스왑하고 화이트보드에 요약 남겨줘.</div>
          </div>

          <div class="msg fromlead">
            <div class="mh conv-row">
              <span class="row-icon"><span class="row-avatar">${ICONS.robot}</span></span>
              <span class="row-content"><b>리드</b></span>
              <span class="row-time"><time class="t">13:41</time></span>
              <span></span>
            </div>
            <div class="bub md">
              <p><code>develop</code> 브랜치에 병합하고 로컬 데몬 바이너리 스왑까지 마쳤습니다. 핵심 변경 이력과 병합 중 올라온 사이즈 가드 확인 건은 화이트보드에 정리해 두었습니다.</p>
            </div>
          </div>

          ${renderWbUpdateCard('real', variantId)}

          <div class="msg fromlead">
            <div class="mh conv-row">
              <span class="row-icon"><span class="row-avatar">${ICONS.robot}</span></span>
              <span class="row-content"><b>리드</b></span>
              <span class="row-time"><time class="t">13:43</time></span>
              <span></span>
            </div>
            <div class="bub md">
              <p>추가로 권한 토글 UX 요약 패널과 워커 1차 리뷰 반영 내역 3건도 같은 토픽에 연속 로그로 이어 붙였습니다. 아래에서 패널 미리보기·연속 로그·정책 캡슐 간 비중을 함께 확인해 주세요.</p>
            </div>
          </div>

          ${renderWbUpdateCard('mixed', variantId)}
        </div>
      </section>`;
  }

  function renderContextView() {
    const v = VARIANTS[state.variant];
    const noteBar = document.getElementById('ctx-note-bar');
    noteBar.innerHTML = `
      <b>${escHtml(v.badge)} · ${escHtml(v.title)}</b>
      <span>${escHtml(v.note)}</span>`;

    const stage = document.getElementById('ctx-stage');
    const showSplit = state.split && state.variant !== 'baseline';
    stage.classList.toggle('is-split', showSplit);

    if (showSplit) {
      stage.innerHTML =
        renderChatColumn('baseline', '현재 (Baseline) — 비교 기준') +
        renderChatColumn(state.variant, `${v.badge} — ${v.title}`);
    } else {
      stage.innerHTML = renderChatColumn(state.variant, `${v.badge} — ${v.title}`);
    }
  }

  function syncControls() {
    document.documentElement.setAttribute('data-theme', state.theme);

    document.querySelectorAll('#ctrl-view .m-opt').forEach(btn => {
      const on = btn.dataset.v === state.view;
      btn.classList.toggle('is-on', on);
      btn.setAttribute('aria-selected', String(on));
    });

    document.querySelectorAll('#ctrl-scenario .m-opt').forEach(btn => {
      const on = btn.dataset.v === state.scenario;
      btn.classList.toggle('is-on', on);
      btn.setAttribute('aria-selected', String(on));
    });

    document.querySelectorAll('#ctrl-theme .m-opt').forEach(btn => {
      const on = btn.dataset.v === state.theme;
      btn.classList.toggle('is-on', on);
      btn.setAttribute('aria-pressed', String(on));
    });

    document.querySelectorAll('#ctrl-variant .ctx-pill').forEach(btn => {
      const on = btn.dataset.v === state.variant;
      btn.classList.toggle('is-on', on);
      btn.setAttribute('aria-selected', String(on));
    });

    const grpScenario = document.getElementById('grp-scenario');
    grpScenario.hidden = state.view !== 'compare';

    const viewCompare = document.getElementById('view-compare');
    const viewContext = document.getElementById('view-context');
    viewCompare.hidden = state.view !== 'compare';
    viewContext.hidden = state.view !== 'context';

    const splitBox = document.getElementById('ctrl-split');
    splitBox.checked = state.split;
    splitBox.disabled = state.variant === 'baseline';
  }

  function render() {
    syncControls();
    if (state.view === 'compare') {
      renderCompareView();
    } else {
      renderContextView();
    }
  }

  // Event wiring
  document.getElementById('ctrl-view').addEventListener('click', e => {
    const btn = e.target.closest('.m-opt');
    if (!btn) return;
    state.view = btn.dataset.v;
    render();
  });

  document.getElementById('ctrl-scenario').addEventListener('click', e => {
    const btn = e.target.closest('.m-opt');
    if (!btn) return;
    state.scenario = btn.dataset.v;
    render();
  });

  document.getElementById('ctrl-theme').addEventListener('click', e => {
    const btn = e.target.closest('.m-opt');
    if (!btn) return;
    state.theme = btn.dataset.v;
    syncControls();
  });

  document.getElementById('ctrl-variant').addEventListener('click', e => {
    const btn = e.target.closest('.ctx-pill');
    if (!btn) return;
    state.variant = btn.dataset.v;
    render();
  });

  document.getElementById('ctrl-split').addEventListener('change', e => {
    state.split = e.target.checked;
    renderContextView();
  });

  // Jump from compare card into 1:1 context view + simulated board jump status feedback
  document.getElementById('m-main').addEventListener('click', e => {
    const openBtn = e.target.closest('[data-open-context]');
    if (openBtn) {
      state.variant = openBtn.dataset.openContext;
      state.view = 'context';
      render();
      window.scrollTo({ top: 0, behavior: 'smooth' });
      return;
    }

    const ref = e.target.closest('.wbref');
    if (ref) {
      e.preventDefault();
      showTopicStatus(ref, `보드 항목으로 이동 미리보기: (${ref.dataset.ref})`);
      return;
    }

    const link = e.target.closest('[data-wb-jump]');
    if (link) {
      e.preventDefault();
      showTopicStatus(link, `보드 이동 미리보기: #${link.dataset.wbJump}`);
      return;
    }

    const el = e.target.closest('[data-wb-element]');
    if (el) {
      showTopicStatus(el, `보드 로그 항목 포커스 미리보기: #${el.dataset.wbElement}`);
    }
  });

  const statusTimers = new WeakMap();
  function showTopicStatus(originEl, message) {
    const topic = originEl.closest('.wbupdate-topic');
    if (!topic) return;
    const status = topic.querySelector('.wbupdate-status');
    if (!status) return;
    status.textContent = message;
    if (statusTimers.has(status)) {
      clearTimeout(statusTimers.get(status));
    }
    statusTimers.set(status, setTimeout(() => {
      status.textContent = '';
    }, 2200));
  }

  // Keyboard shortcuts in 1:1 context view: 0, 1-4 or a-d
  document.addEventListener('keydown', e => {
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    if (e.target && ['INPUT', 'TEXTAREA', 'SELECT'].includes(e.target.tagName)) return;
    if (state.view !== 'context') return;
    const map = { '0': 'baseline', '1': 'a', '2': 'b', '3': 'c', '4': 'd', a: 'a', b: 'b', c: 'c', d: 'd' };
    const next = map[e.key.toLowerCase()];
    if (next && next !== state.variant) {
      state.variant = next;
      render();
    }
  });

  render();
})();
