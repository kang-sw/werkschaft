(() => {
  'use strict';

  const root = document.documentElement;

  // Slot container references
  const slots = {
    topLeft: document.getElementById('slot-top-left'),
    topCenter: document.getElementById('slot-top-center'),
    topRight: document.getElementById('slot-top-right'),
    railHead: document.getElementById('slot-rail-head'),
    railFoot: document.getElementById('slot-rail-foot'),
    botLeft: document.getElementById('slot-bot-left'),
    botCenter: document.getElementById('slot-bot-center'),
    botRight: document.getElementById('slot-bot-right')
  };

  // Real Werkschaft movable controls
  const ctrls = {
    telemetry: document.getElementById('ctrl-session-telemetry'),
    sse: document.getElementById('sse'),
    qsslot: document.getElementById('qsslot'),
    talert: document.getElementById('talert'),
    globalUtils: document.getElementById('ctrl-global-utils'),
    railBatch: document.getElementById('ctrl-rail-batch')
  };

  // Popovers & triggers
  const usageTrigger = document.getElementById('usage-trigger');
  const usagePopover = document.getElementById('usage-popover');
  const gearTrigger = document.getElementById('gear');
  const gmenuPopover = document.getElementById('gmenu');
  const configTrigger = document.getElementById('m-config-trigger');
  const slotPopover = document.getElementById('slot-popover');
  const qsTrigger = document.getElementById('tkqs');
  const qsPopover = document.getElementById('qs-popover');
  const qsInput = document.getElementById('qs-input');
  const alertTrigger = document.getElementById('talert-trigger');
  const alertPopover = document.getElementById('alert-popover');

  const customBadge = document.getElementById('m-custom-badge');
  const wbPresetPill = document.getElementById('wb-preset-pill');
  const liveComboMeta = document.getElementById('live-combo-meta');
  const botGlobalHint = document.getElementById('bot-global-hint');
  const alertToggleBtn = document.getElementById('m-alert-toggle');
  const alertLblEl = document.getElementById('m-alert-lbl');
  const activeRowToggle = document.getElementById('m-active-row-toggle');
  const activeRowLbl = document.getElementById('m-active-row-lbl');
  const wbActiveRowTxt = document.getElementById('wb-active-row-txt');
  const compToggleBtn = document.getElementById('m-comp-toggle');
  const compLblEl = document.getElementById('m-comp-lbl');
  const railToggleBtn = document.getElementById('m-rail-toggle');
  const railToggleLbl = railToggleBtn ? railToggleBtn.querySelector('[data-rail-toggle-lbl]') : null;
  const themeBtn = document.getElementById('m-theme-toggle');
  const themeLblEl = document.getElementById('m-theme-lbl');
  const restoreAlertBtn = document.getElementById('sp-restore-alert');
  const splitter = document.getElementById('rail-splitter');
  const toastEl = document.getElementById('mini-toast');

  // 3 Owner-Focused Presets (Default = Preset 1: Per-Session Top / Global Bottom)
  const PRESETS = {
    '1': {
      name: '프리셋 1 (추천)',
      harness: 'top-left',
      sse: 'top-left',
      search: 'top-center',
      talert: 'bottom-center',
      globalUtils: 'bottom-left',
      railBatch: 'rail-head'
    },
    '2': {
      name: '프리셋 2 (슬림 상단)',
      harness: 'top-slim',
      sse: 'top-dot',
      search: 'top-center',
      talert: 'bottom-center',
      globalUtils: 'bottom-left',
      railBatch: 'rail-head'
    },
    '3': {
      name: '프리셋 3 (레일 푸터 유지)',
      harness: 'top-left',
      sse: 'top-left',
      search: 'top-center',
      talert: 'bottom-center',
      globalUtils: 'rail-foot',
      railBatch: 'rail-head'
    }
  };

  const LOC_META = {
    harness: {
      'top-left': { loc: '상단 좌측', note: '세션 범위 · 브랜치 + 하네스/호스트', global: false },
      'top-slim': { loc: '상단 슬림', note: '세션 범위 · 축약 배지 → 검색 폭 확대', global: false },
      'bottom-left': { loc: '하단 좌측', note: '하단 바 이동 시 상단 타이틀 극최소화', global: false }
    },
    sse: {
      'top-left': { loc: '상단 좌측', note: '세션 범위 · Live 스트림 점 + 라벨', global: false },
      'top-dot': { loc: '상단 점만', note: '세션 범위 · 컴팩트 Live 점 아이콘', global: false },
      'bottom-left': { loc: '하단 좌측', note: '하단 상태 바 좌측 배치', global: false }
    },
    search: {
      'top-center': { loc: '상단 중앙', note: 'IDE 스타일 정중앙 검색 트리거 (⌘K)', global: false },
      'top-right': { loc: '상단 우측', note: '드로어 좌측 컴팩트 검색 버튼', global: false }
    },
    talert: {
      'bottom-center': { loc: '하단 중앙', note: '글로벌 전용 · 타 세션 승인 대기 2건', global: true },
      'bottom-left': { loc: '하단 좌측', note: '글로벌 전용 · 유틸 우측 인접 배치', global: true },
      'top-right': { loc: '상단 우측', note: '상단 드로어 좌측 배치 (비교용)', global: false }
    },
    globalUtils: {
      'bottom-left': { loc: '하단 좌측', note: '글로벌 전용 · .rfoot 이관 → 레일 여유', global: true },
      'rail-foot': { loc: '레일 푸터', note: '레일 하단 보더리스 .rfoot 유지 (A/B)', global: true },
      'bottom-right': { loc: '하단 우측', note: '글로벌 전용 · 사용량 좌측 아이콘화', global: true }
    },
    railBatch: {
      'rail-head': { loc: '레일 헤더', note: '실시간/승인 요약 칩 + #railsleep 유지', global: false },
      'bottom-left': { loc: '하단 좌측', note: '.rhd 초미니멀화 (타이틀·+·새로고침만)', global: false }
    }
  };

  const SLOTS = {
    default: {
      label: '기본 · work',
      short: 'work',
      metrics: { 'cl-wk': 81, 'co-wk': 14, 'wk-5h': 12, 'wk-wk': 46 }
    },
    secondary: {
      label: '보조 · personal',
      short: 'personal',
      metrics: { 'cl-wk': 22, 'co-wk': 5, 'wk-5h': 4, 'wk-wk': 18 }
    },
    team: {
      label: '예비 · team',
      short: 'team',
      metrics: { 'cl-wk': 49, 'co-wk': 28, 'wk-5h': 9, 'wk-wk': 31 }
    }
  };

  const state = {
    harness: 'top-left',
    sse: 'top-left',
    search: 'top-center',
    talert: 'bottom-center',
    globalUtils: 'bottom-left',
    railBatch: 'rail-head'
  };

  let railWidth = 280;
  let themePref = 'dark'; // 'auto' | 'light' | 'dark'
  let toastTimer = null;

  function showToast(msg) {
    if (!toastEl) return;
    toastEl.textContent = msg;
    toastEl.hidden = false;
    if (toastTimer) clearTimeout(toastTimer);
    toastTimer = setTimeout(() => { toastEl.hidden = true; }, 1900);
  }

  function applyRailWidth(px) {
    railWidth = Math.max(220, Math.min(360, Math.round(px)));
    root.style.setProperty('--rail-w', `${railWidth}px`);
  }

  function matchPreset() {
    for (const [key, p] of Object.entries(PRESETS)) {
      if (
        state.harness === p.harness &&
        state.sse === p.sse &&
        state.search === p.search &&
        state.talert === p.talert &&
        state.globalUtils === p.globalUtils &&
        state.railBatch === p.railBatch
      ) {
        return key;
      }
    }
    return null;
  }

  // Re-attach all 6 real movable controls into their target DOM slots in deterministic order
  function renderLayout() {
    // 1. Top-Left movable slot (Per-session telemetry: Branch + Harness + #sse Live)
    slots.topLeft.innerHTML = '';
    if (state.harness === 'top-left' || state.harness === 'top-slim') {
      slots.topLeft.appendChild(ctrls.telemetry);
    }
    if (state.sse === 'top-left' || state.sse === 'top-dot') {
      slots.topLeft.appendChild(ctrls.sse);
    }

    // 2. Top-Center slot (True Visual Center: #qsslot / #tkqs)
    slots.topCenter.innerHTML = '';
    if (state.search === 'top-center') {
      slots.topCenter.appendChild(ctrls.qsslot);
    }

    // 3. Top-Right movable slot (before session drawer handles)
    slots.topRight.innerHTML = '';
    if (state.talert === 'top-right') {
      slots.topRight.appendChild(ctrls.talert);
    }
    if (state.search === 'top-right') {
      slots.topRight.appendChild(ctrls.qsslot);
    }

    // 4. Rail Header & Rail Footer
    slots.railHead.innerHTML = '';
    if (state.railBatch === 'rail-head') {
      slots.railHead.appendChild(ctrls.railBatch);
    }
    slots.railFoot.innerHTML = '';
    if (state.globalUtils === 'rail-foot') {
      slots.railFoot.appendChild(ctrls.globalUtils);
    }

    // 5. Bottom-Left slot (Global Utilities first, then any optional moved items)
    slots.botLeft.innerHTML = '';
    if (state.globalUtils === 'bottom-left') {
      slots.botLeft.appendChild(ctrls.globalUtils);
    }
    if (state.harness === 'bottom-left') {
      slots.botLeft.appendChild(ctrls.telemetry);
    }
    if (state.sse === 'bottom-left') {
      slots.botLeft.appendChild(ctrls.sse);
    }
    if (state.railBatch === 'bottom-left') {
      slots.botLeft.appendChild(ctrls.railBatch);
    }
    if (state.talert === 'bottom-left') {
      slots.botLeft.appendChild(ctrls.talert);
    }

    // 6. Bottom-Center slot (Global #talert)
    slots.botCenter.innerHTML = '';
    if (state.talert === 'bottom-center') {
      slots.botCenter.appendChild(ctrls.talert);
    }

    // 7. Bottom-Right slot (beside locked global .usage-toggle)
    slots.botRight.innerHTML = '';
    if (state.globalUtils === 'bottom-right') {
      slots.botRight.appendChild(ctrls.globalUtils);
    }

    // Subtle fallback hint when bottom-left has no controls (e.g. Preset 3)
    if (botGlobalHint) {
      botGlobalHint.hidden = slots.botLeft.childElementCount > 0;
    }

    // Update root attributes
    const matched = matchPreset();
    const isSlimBottom = matched === '3' || (
      slots.botLeft.childElementCount === 0 && slots.botRight.childElementCount === 0
    );
    root.setAttribute('data-preset', matched || 'custom');
    root.setAttribute('data-slim-bottom', isSlimBottom ? 'true' : 'false');
    root.setAttribute('data-compact-telemetry', state.harness === 'top-slim' ? 'true' : 'false');
    root.setAttribute('data-sse-dot-only', state.sse === 'top-dot' ? 'true' : 'false');

    // Sync tabs & whiteboard preset cards
    document.querySelectorAll('.m-tab[data-preset]').forEach((tab) => {
      const on = tab.dataset.preset === matched;
      tab.classList.toggle('is-on', on);
      tab.setAttribute('aria-selected', on ? 'true' : 'false');
    });
    document.querySelectorAll('.popt[data-preset-pick]').forEach((opt) => {
      opt.classList.toggle('is-picked', opt.dataset.presetPick === matched);
    });

    if (customBadge) customBadge.hidden = Boolean(matched);
    if (wbPresetPill) {
      wbPresetPill.textContent = matched ? `프리셋 ${matched} 활성` : '커스텀 배치';
    }
    if (liveComboMeta) {
      const rfootTxt = state.globalUtils === 'rail-foot' ? '.rfoot 유지' : '.rfoot 하단 이관됨';
      liveComboMeta.textContent = `${matched ? `프리셋 ${matched}` : '커스텀'} · ${rfootTxt}`;
    }

    // Sync configurator pills & whiteboard live matrix
    Object.keys(state).forEach((k) => {
      const val = state[k];
      const group = document.querySelector(`.sp-pills[data-ctrl="${k}"]`);
      if (group) {
        group.querySelectorAll('.sp-pill').forEach((btn) => {
          btn.classList.toggle('is-on', btn.dataset.val === val);
        });
      }
      const meta = LOC_META[k] && LOC_META[k][val];
      if (meta) {
        const locEl = document.querySelector(`[data-loc="${k}"]`);
        const noteEl = document.querySelector(`[data-note="${k}"]`);
        if (locEl) {
          locEl.textContent = meta.loc;
          locEl.classList.toggle('is-global', Boolean(meta.global));
        }
        if (noteEl) noteEl.textContent = meta.note;
      }
    });

    // Reposition open popovers cleanly if any are open
    if (!usagePopover.hidden) positionPopover(usagePopover, usageTrigger, 'up', 'right');
    if (!gmenuPopover.hidden) positionGmenu();
    if (!slotPopover.hidden) positionPopover(slotPopover, configTrigger, 'down', 'left');
    if (!qsPopover.hidden) positionSearchPopover();
    if (!alertPopover.hidden) positionAlertPopover();
  }

  function applyPreset(key) {
    const p = PRESETS[key];
    if (!p) return;
    state.harness = p.harness;
    state.sse = p.sse;
    state.search = p.search;
    state.talert = p.talert;
    state.globalUtils = p.globalUtils;
    state.railBatch = p.railBatch;
    renderLayout();
  }

  // Generic Popover Positioning (Upward or Downward, Viewport-Clamped)
  function closeAllPopovers(except) {
    [
      { el: usagePopover, trig: usageTrigger },
      { el: gmenuPopover, trig: gearTrigger },
      { el: slotPopover, trig: configTrigger },
      { el: qsPopover, trig: qsTrigger },
      { el: alertPopover, trig: alertTrigger }
    ].forEach(({ el, trig }) => {
      if (!el || el === except) return;
      el.hidden = true;
      if (trig) trig.setAttribute('aria-expanded', 'false');
    });
  }

  function positionPopover(popEl, trigEl, dir, align) {
    if (!popEl || !trigEl) return;
    popEl.hidden = false;
    const rect = trigEl.getBoundingClientRect();
    const popRect = popEl.getBoundingClientRect();
    const vw = window.innerWidth;
    const vh = window.innerHeight;
    const gap = 6;

    let left = rect.left;
    if (align === 'right') left = rect.right - popRect.width;
    else if (align === 'center') left = rect.left + (rect.width - popRect.width) / 2;
    left = Math.max(8, Math.min(vw - popRect.width - 8, left));

    let top = dir === 'up' ? rect.top - popRect.height - gap : rect.bottom + gap;
    top = Math.max(8, Math.min(vh - popRect.height - 8, top));

    popEl.style.left = `${Math.round(left)}px`;
    popEl.style.top = `${Math.round(top)}px`;
    trigEl.setAttribute('aria-expanded', 'true');
  }

  function positionGmenu() {
    const align = state.globalUtils === 'bottom-right' ? 'right' : 'left';
    const lbl = document.getElementById('gmenu-anchor-lbl');
    if (lbl) {
      if (state.globalUtils === 'rail-foot') lbl.textContent = '레일 푸터 기준 · 상향 열림';
      else lbl.textContent = '글로벌 하단 바 기준 · 상향 열림';
    }
    positionPopover(gmenuPopover, gearTrigger, 'up', align);
  }

  function positionSearchPopover() {
    const align = state.search === 'top-center' ? 'center' : 'right';
    positionPopover(qsPopover, qsTrigger, 'down', align);
  }

  function positionAlertPopover() {
    const dir = state.talert === 'top-right' ? 'down' : 'up';
    const align = state.talert === 'bottom-center' ? 'center' : (state.talert === 'top-right' ? 'right' : 'left');
    positionPopover(alertPopover, alertTrigger, dir, align);
  }

  // Theme (supports auto / light / dark in #gmenu)
  function setTheme(pref) {
    themePref = pref === 'light' || pref === 'auto' ? pref : 'dark';
    const effective = themePref === 'auto'
      ? (window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark')
      : themePref;

    root.setAttribute('data-theme', effective);
    if (themeLblEl) themeLblEl.textContent = effective === 'dark' ? 'Dark' : 'Light';
    if (themeBtn) themeBtn.setAttribute('aria-pressed', effective === 'light' ? 'true' : 'false');
    document.querySelectorAll('[data-gm-theme]').forEach((b) => {
      const on = b.dataset.gmTheme === themePref;
      b.classList.toggle('is-on', on);
      b.setAttribute('aria-pressed', on ? 'true' : 'false');
    });
  }

  function setAlertVisible(on) {
    root.setAttribute('data-alert', on ? 'on' : 'off');
    ctrls.talert.hidden = !on;
    if (!on && !alertPopover.hidden) closeAllPopovers();
    if (alertToggleBtn) {
      alertToggleBtn.classList.toggle('is-on', on);
      alertToggleBtn.setAttribute('aria-pressed', on ? 'true' : 'false');
    }
    if (alertLblEl) alertLblEl.textContent = on ? '전역 알림 ON' : '전역 알림 OFF';
    if (restoreAlertBtn) restoreAlertBtn.hidden = on;
  }

  function setActiveRow(mode) {
    const next = mode === 'neon' ? 'neon' : 'tonal';
    root.setAttribute('data-active-row', next);
    if (activeRowToggle) activeRowToggle.setAttribute('aria-pressed', next === 'neon' ? 'true' : 'false');
    if (activeRowLbl) activeRowLbl.textContent = next === 'neon' ? '활성행: 네온' : '활성행: 톤+바';
    if (wbActiveRowTxt) wbActiveRowTxt.textContent = next === 'neon' ? '네온 링 (기존)' : '톤+바 (Borderless)';
  }

  function setCompanion(on) {
    root.setAttribute('data-companion', on ? 'on' : 'off');
    if (compToggleBtn) {
      compToggleBtn.classList.toggle('is-on', on);
      compToggleBtn.setAttribute('aria-pressed', on ? 'true' : 'false');
    }
    if (compLblEl) compLblEl.textContent = on ? '컴패니언 ON' : '컴패니언 OFF';
  }

  function setRailCollapsed(collapsed) {
    root.setAttribute('data-rail-collapsed', collapsed ? 'true' : 'false');
    if (railToggleBtn) railToggleBtn.setAttribute('aria-pressed', collapsed ? 'true' : 'false');
    if (railToggleLbl) railToggleLbl.textContent = collapsed ? '레일 펼치기' : '레일 접기';
    const railbtn = document.getElementById('railbtn');
    if (railbtn) railbtn.setAttribute('aria-expanded', collapsed ? 'false' : 'true');
  }

  function selectSession(row) {
    if (!row) return;
    document.querySelectorAll('.rail .rs').forEach((el) => {
      const on = el === row;
      el.classList.toggle('cur', on);
      if (on) el.setAttribute('aria-current', 'page');
      else el.removeAttribute('aria-current');
    });

    const name = row.dataset.name || '기능 2';
    const seq = row.dataset.seq || '#42';
    const parent = row.dataset.parent || '';
    const ws = row.dataset.ws || 'devenv';
    const branch = row.dataset.branch || 'develop';
    const harness = row.dataset.harness || 'agy';
    const harnessLabel = row.dataset.harnessLabel || 'agy · high';

    const nameEl = document.getElementById('top-session-name');
    const seqEl = document.getElementById('top-session-seq');
    const wsEl = document.getElementById('top-ws-name');
    const parentBtn = document.getElementById('btn-parent');
    const parentNameEl = document.getElementById('top-parent-name');
    const crumbSep = document.getElementById('top-crumb-sep');
    const ctxEl = document.getElementById('chat-session-ctx');
    const branchEl = document.getElementById('worktree-branch-lbl');
    const branchWrap = document.getElementById('worktree-path');
    const hbEl = document.getElementById('top-harness-hb');
    const hsLblEl = document.getElementById('top-harness-lbl');
    const hsBadge = document.getElementById('ctrl-harness-badge');

    if (nameEl) nameEl.textContent = name;
    if (seqEl) seqEl.textContent = seq;
    if (wsEl) wsEl.textContent = ws;
    if (ctxEl) ctxEl.textContent = `${name} · ${seq}`;
    if (branchEl) branchEl.textContent = branch;
    if (branchWrap) branchWrap.title = `현재 워크트리 브랜치: ${branch}`;
    if (hbEl) hbEl.setAttribute('data-h', harness);
    if (hsLblEl) hsLblEl.textContent = harnessLabel;
    if (hsBadge) hsBadge.title = `현재 세션 하네스 · 호스트 (${harnessLabel})`;

    const hasParent = Boolean(parent);
    if (parentBtn) parentBtn.style.display = hasParent ? '' : 'none';
    if (crumbSep) crumbSep.style.display = hasParent ? '' : 'none';
    if (parentNameEl && hasParent) parentNameEl.textContent = parent;
  }

  function selectSlot(slotKey) {
    const slot = SLOTS[slotKey];
    if (!slot) return;
    document.querySelectorAll('.slot-pill').forEach((btn) => {
      const on = btn.dataset.slot === slotKey;
      btn.classList.toggle('is-on', on);
      btn.setAttribute('aria-checked', on ? 'true' : 'false');
    });
    const caption = document.getElementById('up-slot-caption');
    if (caption) caption.textContent = `활성 슬롯: ${slot.label}`;
    const chip = document.getElementById('top-slot-chip');
    if (chip) chip.textContent = slot.short;

    Object.entries(slot.metrics).forEach(([k, val]) => {
      document.querySelectorAll(`[data-u="${k}"]`).forEach((el) => { el.textContent = `${val}%`; });
      document.querySelectorAll(`[data-up-val="${k}"]`).forEach((el) => { el.textContent = `${val}%`; });
      document.querySelectorAll(`[data-up-bar="${k}"]`).forEach((bar) => {
        bar.style.width = `${Math.max(3, val)}%`;
      });
    });
  }

  function filterQuickSearch(query) {
    const q = (query || '').trim().toLowerCase();
    document.querySelectorAll('#qs-list .qs-item').forEach((item) => {
      const hay = `${item.dataset.qsText || ''} ${item.textContent || ''}`.toLowerCase();
      item.hidden = q.length > 0 && !hay.includes(q);
    });
  }

  // Event Bindings — Presets & Configurator
  document.querySelectorAll('.m-tab[data-preset]').forEach((tab) => {
    tab.addEventListener('click', () => applyPreset(tab.dataset.preset));
  });
  document.querySelectorAll('.popt[data-preset-pick]').forEach((opt) => {
    opt.addEventListener('click', () => applyPreset(opt.dataset.presetPick));
  });

  document.querySelectorAll('.sp-pills').forEach((group) => {
    const ctrlKey = group.dataset.ctrl;
    group.querySelectorAll('.sp-pill').forEach((btn) => {
      btn.addEventListener('click', () => {
        state[ctrlKey] = btn.dataset.val;
        renderLayout();
      });
    });
  });

  document.getElementById('sp-reset-rec')?.addEventListener('click', () => applyPreset('1'));
  document.getElementById('wb-open-config')?.addEventListener('click', (e) => {
    e.stopPropagation();
    closeAllPopovers(slotPopover);
    positionPopover(slotPopover, configTrigger, 'down', 'left');
  });

  // Popover toggles
  usageTrigger?.addEventListener('click', (e) => {
    e.stopPropagation();
    const wasOpen = !usagePopover.hidden;
    closeAllPopovers();
    if (!wasOpen) positionPopover(usagePopover, usageTrigger, 'up', 'right');
  });
  gearTrigger?.addEventListener('click', (e) => {
    e.stopPropagation();
    const wasOpen = !gmenuPopover.hidden;
    closeAllPopovers();
    if (!wasOpen) positionGmenu();
  });
  configTrigger?.addEventListener('click', (e) => {
    e.stopPropagation();
    const wasOpen = !slotPopover.hidden;
    closeAllPopovers();
    if (!wasOpen) positionPopover(slotPopover, configTrigger, 'down', 'left');
  });
  qsTrigger?.addEventListener('click', (e) => {
    e.stopPropagation();
    const wasOpen = !qsPopover.hidden;
    closeAllPopovers();
    if (!wasOpen) {
      positionSearchPopover();
      if (qsInput) {
        qsInput.value = '';
        filterQuickSearch('');
        qsInput.focus();
      }
    }
  });
  alertTrigger?.addEventListener('click', (e) => {
    e.stopPropagation();
    const wasOpen = !alertPopover.hidden;
    closeAllPopovers();
    if (!wasOpen) positionAlertPopover();
  });

  document.getElementById('up-close')?.addEventListener('click', () => closeAllPopovers());
  document.getElementById('gmenu-close')?.addEventListener('click', () => closeAllPopovers());
  document.getElementById('sp-close')?.addEventListener('click', () => closeAllPopovers());
  document.getElementById('alert-close')?.addEventListener('click', () => closeAllPopovers());

  document.addEventListener('click', (e) => {
    const inside = [
      usagePopover, gmenuPopover, slotPopover, qsPopover, alertPopover,
      usageTrigger, gearTrigger, configTrigger, qsTrigger, alertTrigger
    ].some((el) => el && el.contains(e.target));
    if (!inside) closeAllPopovers();
  });

  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') closeAllPopovers();
    if ((e.ctrlKey || e.metaKey) && e.key === '`') {
      e.preventDefault();
      setRailCollapsed(root.getAttribute('data-rail-collapsed') !== 'true');
    }
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
      e.preventDefault();
      closeAllPopovers();
      positionSearchPopover();
      if (qsInput) {
        qsInput.value = '';
        filterQuickSearch('');
        qsInput.focus();
      }
    }
  });

  window.addEventListener('resize', () => {
    if (!usagePopover.hidden) positionPopover(usagePopover, usageTrigger, 'up', 'right');
    if (!gmenuPopover.hidden) positionGmenu();
    if (!slotPopover.hidden) positionPopover(slotPopover, configTrigger, 'down', 'left');
    if (!qsPopover.hidden) positionSearchPopover();
    if (!alertPopover.hidden) positionAlertPopover();
  });

  // Quick Search interactive items
  qsInput?.addEventListener('input', () => filterQuickSearch(qsInput.value));
  document.querySelectorAll('#qs-list .qs-item').forEach((item) => {
    item.addEventListener('click', () => {
      const sid = item.dataset.qsSession;
      if (sid) {
        const row = document.querySelector(`.rail .rs[data-session-id="${sid}"]`);
        if (row) selectSession(row);
        showToast(`'${row ? row.dataset.name : sid}' 세션으로 전환됨`);
      } else {
        showToast(`${item.dataset.qsKind || '항목'} 열기`);
      }
      closeAllPopovers();
    });
  });

  // Alert popover & dismiss/restore actions
  document.querySelectorAll('[data-al-jump]').forEach((btn) => {
    btn.addEventListener('click', () => {
      const sid = btn.dataset.alJump;
      const row = document.querySelector(`.rail .rs[data-session-id="${sid}"]`);
      if (row) selectSession(row);
      closeAllPopovers();
      showToast(`'${row ? row.dataset.name : sid}' 세션으로 이동 (승인 대기)`);
    });
  });
  document.getElementById('al-ack-all')?.addEventListener('click', () => {
    setAlertVisible(false);
    showToast('전역 알림을 확인 처리했습니다 (상단 바에서 다시 켤 수 있음)');
  });
  document.getElementById('talert-dismiss')?.addEventListener('click', (e) => {
    e.stopPropagation();
    setAlertVisible(false);
    showToast('전역 알림 숨김 (상단 "전역 알림" 토글로 복원 가능)');
  });
  restoreAlertBtn?.addEventListener('click', () => setAlertVisible(true));

  // Account slot switcher
  document.querySelectorAll('.slot-pill').forEach((btn) => {
    btn.addEventListener('click', () => selectSlot(btn.dataset.slot));
  });

  // #gmenu interactive controls
  document.querySelectorAll('[data-gm-theme]').forEach((b) => {
    b.addEventListener('click', () => setTheme(b.dataset.gmTheme));
  });
  document.querySelectorAll('[data-gm-input]').forEach((b) => {
    b.addEventListener('click', () => {
      document.querySelectorAll('[data-gm-input]').forEach((x) => {
        const on = x === b;
        x.classList.toggle('is-on', on);
        x.setAttribute('aria-pressed', on ? 'true' : 'false');
      });
    });
  });
  document.querySelectorAll('[data-gm-motion]').forEach((b) => {
    b.addEventListener('click', () => {
      const m = b.dataset.gmMotion;
      root.setAttribute('data-motion', m);
      document.querySelectorAll('[data-gm-motion]').forEach((x) => {
        const on = x === b;
        x.classList.toggle('is-on', on);
        x.setAttribute('aria-pressed', on ? 'true' : 'false');
      });
    });
  });
  document.getElementById('gm-copy-login')?.addEventListener('click', () => {
    closeAllPopovers();
    showToast('로그인 링크가 클립보드에 복사되었습니다');
  });
  document.getElementById('gm-open-settings')?.addEventListener('click', () => {
    closeAllPopovers();
    showToast('전역 워크플로 설정 (/settings) 열기');
  });

  // Preview bar toggles
  alertToggleBtn?.addEventListener('click', () => {
    setAlertVisible(root.getAttribute('data-alert') !== 'on');
  });
  activeRowToggle?.addEventListener('click', () => {
    setActiveRow(root.getAttribute('data-active-row') === 'tonal' ? 'neon' : 'tonal');
  });
  compToggleBtn?.addEventListener('click', () => {
    setCompanion(root.getAttribute('data-companion') !== 'on');
  });
  themeBtn?.addEventListener('click', () => {
    setTheme(root.getAttribute('data-theme') === 'dark' ? 'light' : 'dark');
  });
  const toggleRail = () => setRailCollapsed(root.getAttribute('data-rail-collapsed') !== 'true');
  railToggleBtn?.addEventListener('click', toggleRail);
  document.querySelectorAll('[data-action="toggle-rail"]').forEach((btn) => {
    btn.addEventListener('click', toggleRail);
  });

  // Real controls subtle feedback
  document.getElementById('top-session-name')?.addEventListener('click', () => {
    showToast('메일박스 주소 복사됨: id:feat2');
  });
  document.getElementById('btn-rename')?.addEventListener('click', () => showToast('세션 이름 인라인 편집'));
  document.getElementById('btn-home')?.addEventListener('click', () => showToast('전역 홈 랜딩으로 이동'));
  document.getElementById('btn-config-global')?.addEventListener('click', () => showToast('전역 워크플로 설정 (/settings)'));
  document.getElementById('thandle')?.addEventListener('click', () => showToast('티켓 맵 드로어 토글'));
  document.getElementById('tk-open')?.addEventListener('click', () => showToast('티켓 트래커 열기 (열린 티켓 3)'));
  document.getElementById('tkadd')?.addEventListener('click', () => showToast('새 티켓 작성'));
  document.getElementById('drhandle')?.addEventListener('click', () => showToast('패널 · Git 드로어 토글'));
  document.getElementById('railsleep')?.addEventListener('click', () => showToast('유휴 세션 절전 확인'));
  document.getElementById('railref')?.addEventListener('click', () => showToast('세션 목록 새로고침 완료'));
  document.getElementById('comp-msg-btn')?.addEventListener('click', () => {
    const badge = document.getElementById('comp-unread');
    const speech = document.getElementById('comp-speech');
    if (badge && !badge.hidden) {
      badge.hidden = true;
      if (speech) speech.textContent = '"새 메신저 팝업이 곧 열려요!"';
      showToast('컴패니언 메신저 트리거 (미읽음 1 확인됨)');
    } else {
      showToast('컴패니언 메신저 팝업 트리거');
    }
  });

  // Rail interactions
  document.querySelectorAll('.rws > .wsbar').forEach((bar) => {
    bar.addEventListener('click', () => {
      const ws = bar.closest('.rws');
      if (!ws) return;
      const collapsed = ws.classList.toggle('is-col');
      bar.setAttribute('aria-expanded', collapsed ? 'false' : 'true');
    });
  });
  document.querySelectorAll('.rfh').forEach((btn) => {
    btn.addEventListener('click', () => {
      const expanded = btn.getAttribute('aria-expanded') === 'true';
      document.querySelectorAll('.rfh').forEach((b) => b.setAttribute('aria-expanded', 'false'));
      if (!expanded) btn.setAttribute('aria-expanded', 'true');
    });
  });
  document.querySelectorAll('.rail .rs').forEach((row) => {
    row.addEventListener('click', () => selectSession(row));
  });
  document.getElementById('btn-parent')?.addEventListener('click', () => {
    const mainRow = document.querySelector('.rail .rs[data-session-id="main"]');
    if (mainRow) selectSession(mainRow);
  });

  // Draggable rail splitter (220px–360px)
  if (splitter) {
    let startX = 0;
    let startW = railWidth;
    let dragging = false;

    splitter.addEventListener('pointerdown', (e) => {
      dragging = true;
      startX = e.clientX;
      startW = railWidth;
      splitter.classList.add('is-dragging');
      document.body.classList.add('is-resizing');
      splitter.setPointerCapture(e.pointerId);
    });
    splitter.addEventListener('pointermove', (e) => {
      if (!dragging) return;
      applyRailWidth(startW + (e.clientX - startX));
    });
    const endDrag = (e) => {
      if (!dragging) return;
      dragging = false;
      splitter.classList.remove('is-dragging');
      document.body.classList.remove('is-resizing');
      if (e && splitter.hasPointerCapture(e.pointerId)) splitter.releasePointerCapture(e.pointerId);
    };
    splitter.addEventListener('pointerup', endDrag);
    splitter.addEventListener('pointercancel', endDrag);
    splitter.addEventListener('keydown', (e) => {
      if (e.key === 'ArrowLeft') { e.preventDefault(); applyRailWidth(railWidth - 12); }
      if (e.key === 'ArrowRight') { e.preventDefault(); applyRailWidth(railWidth + 12); }
    });
  }

  // Boot + Query Param Hooks for Headless Chrome Verification
  const params = new URLSearchParams(window.location.search);
  const qPreset = params.get('preset');
  const qTheme = params.get('theme');
  const qActive = params.get('active');
  const qComp = params.get('comp');
  const qAlert = params.get('alert');
  const qRail = params.get('rail');
  const qRailW = Number(params.get('railw'));
  const qPop = params.get('pop');
  const qUnreg = params.get('unreg');

  root.classList.add('is-booting');
  if (qRailW >= 220 && qRailW <= 360) applyRailWidth(qRailW);
  applyPreset(PRESETS[qPreset] ? qPreset : '1');
  setTheme(qTheme === 'light' ? 'light' : 'dark');
  setActiveRow(qActive === 'neon' ? 'neon' : 'tonal');
  setCompanion(qComp !== 'off');
  setAlertVisible(qAlert !== 'off');
  setRailCollapsed(qRail === 'collapsed');
  if (qUnreg === 'open') {
    const unregEl = document.getElementById('rws-unreg');
    if (unregEl) unregEl.open = true;
  }
  void root.offsetWidth;
  root.classList.remove('is-booting');

  if (qPop === 'usage') {
    requestAnimationFrame(() => positionPopover(usagePopover, usageTrigger, 'up', 'right'));
  } else if (qPop === 'config') {
    requestAnimationFrame(() => positionPopover(slotPopover, configTrigger, 'down', 'left'));
  } else if (qPop === 'gear') {
    requestAnimationFrame(() => positionGmenu());
  } else if (qPop === 'search') {
    requestAnimationFrame(() => positionSearchPopover());
  } else if (qPop === 'alert') {
    requestAnimationFrame(() => positionAlertPopover());
  }
})();
